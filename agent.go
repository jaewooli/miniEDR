package miniedr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/jaewooli/miniedr/capturer"
)

// EDRAgent runs captures per schedule (different intervals per capturer).
type EDRAgent struct {
	Schedules       []CapturerSchedule
	DefaultInterval time.Duration
	Out             io.Writer
	Verbose         bool
}

func NewEDRAgent(schedules []CapturerSchedule) *EDRAgent {
	return &EDRAgent{
		Schedules:       schedules,
		DefaultInterval: 3 * time.Second,
		Out:             os.Stdout,
	}
}

// Run launches one goroutine per schedule and blocks until ctx is done or an error occurs.
func (a *EDRAgent) Run(ctx context.Context) error {
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

	var ctxErr error
	for {
		select {
		case err, ok := <-errCh:
			if ok && err != nil {
				return err
			}
			if !ok {
				if ctxErr != nil {
					return ctxErr
				}
				return nil
			}
		case <-ctx.Done():
			ctxErr = ctx.Err()
		}
	}
}

func (a *EDRAgent) runSchedule(ctx context.Context, sc CapturerSchedule) error {
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

func (a *EDRAgent) captureOnce(c capturer.Capturer) error {
	if err := c.Capture(); err != nil {
		fmt.Fprintf(a.Out, "[%s] capture error: %v\n", capturer.CapturerName(c), err)
		return err
	}

	info, err := c.GetInfo()
	if err != nil {
		fmt.Fprintf(a.Out, "[%s] getinfo error: %v\n", capturer.CapturerName(c), err)
		return err
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
