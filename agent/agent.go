package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
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
	}
}

// Run launches one goroutine per schedule and blocks until ctx is done or an error occurs.
func (a *CollectAgent) Run(ctx context.Context) error {
	if len(a.Schedules) == 0 {
		return errors.New("edr agent: schedules is empty")
	}
	if a.Out == nil {
		a.Out = io.Discard
	}
	if a.DefaultInterval <= 0 {
		a.DefaultInterval = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(a.Schedules))
	var wg sync.WaitGroup

	for _, sc := range a.Schedules {
		wg.Add(1)
		go func(sc CapturerSchedule) {
			defer wg.Done()
			if err := a.runSchedule(ctx, sc); err != nil {
				errCh <- err
				cancel()
			}
		}(sc)
	}

	go func() {
		wg.Wait()
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (a *CollectAgent) runSchedule(ctx context.Context, sc CapturerSchedule) error {
	if sc.Capturer == nil {
		return errors.New("edr agent: capturer is nil")
	}
	interval := sc.Interval
	if interval <= 0 {
		interval = a.DefaultInterval
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}

	if err := a.captureOnce(sc.Capturer); err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := a.captureOnce(sc.Capturer); err != nil {
				return err
			}
		}
	}
}

func (a *CollectAgent) captureOnce(c capturer.Capturer) error {
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
	info.Meta.Host = a.hostName
	info.Meta.AgentVersion = AgentVersion
	info.Meta.Session = a.sessionID
	info.Meta.Timezone = a.timezone
	if info.Meta.CapturedAt.IsZero() {
		info.Meta.CapturedAt = time.Now()
	}

	for _, sink := range a.Sinks {
		if sink == nil {
			continue
		}
		if err := sink.Consume(info); err != nil {
			fmt.Fprintf(a.Out, "[%s] sink error: %v\n", capturer.CapturerName(c), err)
		}
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
	return nil
}

func newSessionID() string {
	now := time.Now().UnixNano()
	u, _ := user.Current()
	return fmt.Sprintf("%s-%d", u.Username, now)
}

// AddSink registers a telemetry sink to receive captured InfoData.
func (a *CollectAgent) AddSink(s miniedr.TelemetrySink) {
	if s == nil {
		return
	}
	a.Sinks = append(a.Sinks, s)
}
