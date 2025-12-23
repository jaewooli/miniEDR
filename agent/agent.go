package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"reflect"
	"runtime"
	"sync"
	"time"

	"github.com/jaewooli/miniedr"
	"github.com/jaewooli/miniedr/capturer"
)

const AgentVersion = "dev"

// CapturerSchedule binds a capturer to a capture interval.
type CapturerSchedule = miniedr.CapturerSchedule

// CollectAgent runs captures per schedule (different intervals per capturer).
type CollectAgent struct {
	Schedules       []CapturerSchedule
	DefaultInterval time.Duration
	Out             io.Writer
	Verbose         bool

	hostName  string
	timezone  string
	sessionID string
	Sinks     []miniedr.TelemetrySink
	Pipeline  *miniedr.AlertPipeline

	mu         sync.Mutex
	processMu  sync.Mutex
	Errs       []error
	sinkStats  map[string]*sinkStat
	pipelineCh chan queueItem
	pipelineWG sync.WaitGroup
	queueSize  int
	queueDrops int
}

func NewCollectAgent(schedules []CapturerSchedule) *CollectAgent {
	host, _ := os.Hostname()
	tz := time.Now().Format("-0700")
	return &CollectAgent{
		Schedules:       schedules,
		DefaultInterval: 3 * time.Second,
		Out:             os.Stdout,
		hostName:        host,
		timezone:        tz,
		sessionID:       newSessionID(),
		sinkStats:       map[string]*sinkStat{},
		queueSize:       64,
	}
}

// Run launches one goroutine per schedule and blocks until ctx is done or an error occurs.
func (a *CollectAgent) Run(ctx context.Context) error {
	if len(a.Schedules) == 0 {
		return errors.New("edr agent: schedules is empty")
	}
	for i, sc := range a.Schedules {
		if sc.Capturer == nil {
			return fmt.Errorf("edr agent: schedule %d capturer is nil", i)
		}
	}
	if a.Out == nil {
		a.Out = io.Discard
	}
	if a.DefaultInterval <= 0 {
		a.DefaultInterval = 5 * time.Second
	}
	if a.sinkStats == nil {
		a.sinkStats = map[string]*sinkStat{}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if a.pipelineCh == nil {
		if a.queueSize <= 0 {
			a.queueSize = 64
		}
		a.pipelineCh = make(chan queueItem, a.queueSize)
	}
	a.pipelineWG.Add(1)
	go func() {
		defer a.pipelineWG.Done()
		a.pipelineLoop(ctx)
	}()

	var wg sync.WaitGroup
	for _, sc := range a.Schedules {
		wg.Add(1)
		go func(sc CapturerSchedule) {
			defer wg.Done()
			a.runSchedule(ctx, sc)
		}(sc)
	}

	<-ctx.Done()
	wg.Wait()
	a.pipelineWG.Wait()
	return a.firstErrorOr(ctx.Err())
}

func (a *CollectAgent) runSchedule(ctx context.Context, sc CapturerSchedule) {
	interval := sc.Interval
	if interval <= 0 {
		interval = a.DefaultInterval
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}

	if err := a.captureOnce(sc.Capturer, interval); err != nil {
		a.recordErr(err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.captureOnce(sc.Capturer, interval); err != nil {
				a.recordErr(err)
			}
		}
	}
}

func (a *CollectAgent) captureOnce(c capturer.Capturer, interval time.Duration) error {
	if err := c.Capture(); err != nil {
		fmt.Fprintf(a.Out, "[%s] capture error: %v\n", capturer.CapturerName(c), err)
		return err
	}

	info, err := c.GetInfo()
	if err != nil {
		fmt.Fprintf(a.Out, "[%s] getinfo error: %v\n", capturer.CapturerName(c), err)
		return err
	}

	// standardize telemetry meta
	info.Meta.Host = firstNonEmpty(info.Meta.Host, a.hostName)
	info.Meta.AgentVersion = firstNonEmpty(info.Meta.AgentVersion, AgentVersion)
	info.Meta.AgentBuild = firstNonEmpty(info.Meta.AgentBuild, AgentVersion)
	info.Meta.Session = firstNonEmpty(info.Meta.Session, a.sessionID)
	info.Meta.Timezone = firstNonEmpty(info.Meta.Timezone, a.timezone)
	info.Meta.OS = firstNonEmpty(info.Meta.OS, runtime.GOOS)
	info.Meta.Arch = firstNonEmpty(info.Meta.Arch, runtime.GOARCH)
	info.Meta.Capturer = firstNonEmpty(info.Meta.Capturer, capturer.CapturerName(c))
	if info.Meta.IntervalSec == 0 && interval > 0 {
		info.Meta.IntervalSec = interval.Seconds()
	}
	if info.Meta.CapturedAt.IsZero() {
		info.Meta.CapturedAt = time.Now()
	}

	fmt.Fprintf(a.Out, "[%s] %s\n", capturer.CapturerName(c), info.Summary)

	if a.Verbose {
		verboseInfo := info.Summary
		if vc, ok := c.(capturer.VerboseInfo); ok {
			vi, err := vc.GetVerboseInfo()
			if err != nil {
				fmt.Fprintf(a.Out, "[%s] getverboseinfo error: %v\n", capturer.CapturerName(c), err)
				return err
			}
			verboseInfo = vi
		}

		ts := time.Now().Format(time.RFC3339)
		fmt.Fprintf(a.Out, "\n==== %s (verbose) @ %s ====\n%s\n", capturer.CapturerName(c), ts, verboseInfo)
	}

	name := capturer.CapturerName(c)
	if a.pipelineCh != nil {
		select {
		case a.pipelineCh <- queueItem{name: name, info: info}:
			// queued
		default:
			// queue full, process synchronously to avoid loss
			fmt.Fprintf(a.Out, "[%s] pipeline queue full; processing inline\n", name)
			a.incrementDrop()
			a.processInfo(name, info)
		}
	} else {
		a.processInfo(name, info)
	}
	return nil
}

func newSessionID() string {
	now := time.Now().UnixNano()
	u, _ := user.Current()
	name := "unknown"
	if u != nil && u.Username != "" {
		name = u.Username
	}
	return fmt.Sprintf("%s-%d", name, now)
}

// AddSink registers a telemetry sink to receive captured InfoData.
func (a *CollectAgent) AddSink(s miniedr.TelemetrySink) {
	if s == nil {
		return
	}
	a.Sinks = append(a.Sinks, s)
}

// AddResponder registers an alert responder for detections.
func (a *CollectAgent) AddResponder(r miniedr.AlertResponder) {
	if r == nil {
		return
	}
	if a.Pipeline == nil {
		a.Pipeline = &miniedr.AlertPipeline{}
	}
	if a.Pipeline.Responder == nil {
		a.Pipeline.Responder = &miniedr.ResponderPipeline{}
	}
	a.Pipeline.Responder.Responders = append(a.Pipeline.Responder.Responders, r)
}

func (a *CollectAgent) recordErr(err error) {
	if err == nil {
		return
	}
	a.mu.Lock()
	a.Errs = append(a.Errs, err)
	a.mu.Unlock()
}

func (a *CollectAgent) firstErrorOr(fallback error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.Errs) > 0 {
		return a.Errs[0]
	}
	return fallback
}

// sinkStat tracks success/failure counts and last error per sink.
type sinkStat struct {
	Success int
	Failure int
	LastErr error
}

// SinkStats returns a snapshot of sink outcomes keyed by sink type name.
func (a *CollectAgent) SinkStats() map[string]sinkStat {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]sinkStat, len(a.sinkStats))
	for k, v := range a.sinkStats {
		out[k] = *v
	}
	return out
}

func (a *CollectAgent) recordSinkResult(s miniedr.TelemetrySink, err error) {
	if s == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	name := sinkName(s)
	stat, ok := a.sinkStats[name]
	if !ok {
		stat = &sinkStat{}
		a.sinkStats[name] = stat
	}
	if err != nil {
		stat.Failure++
		stat.LastErr = err
		a.Errs = append(a.Errs, err)
		return
	}
	stat.Success++
	stat.LastErr = nil
}

func sinkName(s miniedr.TelemetrySink) string {
	t := reflect.TypeOf(s)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Name()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (a *CollectAgent) processInfo(name string, info capturer.InfoData) {
	a.processMu.Lock()
	defer a.processMu.Unlock()

	for _, sink := range a.Sinks {
		if sink == nil {
			continue
		}
		if err := sink.Consume(info); err != nil {
			fmt.Fprintf(a.Out, "[%s] sink error: %v\n", name, err)
			a.recordSinkResult(sink, err)
			continue
		}
		a.recordSinkResult(sink, nil)
	}

	if a.Pipeline != nil {
		alerts, errs := a.Pipeline.Process(info)
		for _, er := range errs {
			fmt.Fprintf(a.Out, "[%s] responder error: %v\n", name, er)
			a.recordErr(er)
		}
		if len(alerts) > 0 && a.Verbose {
			fmt.Fprintf(a.Out, "[%s] alerts: %d\n", name, len(alerts))
		}
	}
}

func (a *CollectAgent) pipelineLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-a.pipelineCh:
			a.processInfo(item.name, item.info)
		}
	}
}

type queueItem struct {
	name string
	info capturer.InfoData
}

func (a *CollectAgent) incrementDrop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.queueDrops++
}

// QueueStats returns current queue size, capacity, and drops.
func (a *CollectAgent) QueueStats() (pending int, capacity int, drops int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	capacity = cap(a.pipelineCh)
	if a.pipelineCh != nil {
		pending = len(a.pipelineCh)
	}
	drops = a.queueDrops
	return
}
