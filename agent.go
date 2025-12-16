package miniedr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// EDRAgent runs SnapshotManager captures at a fixed interval.
type EDRAgent struct {
	Manager  *SnapshotManager
	Interval time.Duration
	Out      io.Writer
}

func NewEDRAgent(sm *SnapshotManager) *EDRAgent {
	return &EDRAgent{
		Manager:  sm,
		Interval: 5 * time.Second,
		Out:      os.Stdout,
	}
}

// Run blocks until ctx is done. It captures immediately once, then every interval.
func (a *EDRAgent) Run(ctx context.Context) error {
	if a.Manager == nil {
		return errors.New("edr agent: Manager is nil")
	}
	if a.Interval <= 0 {
		a.Interval = 5 * time.Second
	}
	if a.Out == nil {
		a.Out = io.Discard
	}

	if err := a.captureOnce(); err != nil {
		return err
	}

	ticker := time.NewTicker(a.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := a.captureOnce(); err != nil {
				return err
			}
		}
	}
}

func (a *EDRAgent) captureOnce() error {
	if err := a.Manager.Capture(); err != nil {
		fmt.Fprintf(a.Out, "capture error: %v\n", err)
		return err
	}
	info, err := a.Manager.GetInfo()
	if err != nil {
		fmt.Fprintf(a.Out, "getinfo error: %v\n", err)
		return err
	}
	fmt.Fprintln(a.Out, info)
	return nil
}
