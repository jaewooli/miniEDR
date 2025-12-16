package miniedr_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jaewooli/miniedr"
)

type stubEDRCapturer struct {
	captureErr error
	info       string
	infoErr    error
	capCalls   int
	infoCalls  int
}

func (s *stubEDRCapturer) Capture() error {
	s.capCalls++
	return s.captureErr
}

func (s *stubEDRCapturer) GetInfo() (string, error) {
	s.infoCalls++
	return s.info, s.infoErr
}

func newStubSnapshotManager(out *bytes.Buffer, captureErr, infoErr error, info string) (*miniedr.SnapshotManager, *stubEDRCapturer) {
	stub := &stubEDRCapturer{
		captureErr: captureErr,
		infoErr:    infoErr,
		info:       info,
	}
	sm := miniedr.NewSnapshotManager(out, []miniedr.Capturer{stub})
	return sm, stub
}

func TestEDRAgentRun(t *testing.T) {
	t.Run("runs until context done", func(t *testing.T) {
		buf := &bytes.Buffer{}
		sm, stub := newStubSnapshotManager(buf, nil, nil, "ok")
		agent := &miniedr.EDRAgent{
			Manager:  sm,
			Interval: 5 * time.Millisecond,
			Out:      buf,
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()

		err := agent.Run(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("want context deadline error, got %v", err)
		}

		assertTrue(t, stub.capCalls >= 2)
		assertTrue(t, stub.infoCalls >= 2)
		assertTrue(t, strings.Count(buf.String(), "ok") >= 2)
	})

	t.Run("returns capture error immediately", func(t *testing.T) {
		buf := &bytes.Buffer{}
		sm, stub := newStubSnapshotManager(buf, errors.New("kaput"), nil, "ok")
		agent := &miniedr.EDRAgent{
			Manager:  sm,
			Interval: 5 * time.Millisecond,
			Out:      buf,
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err := agent.Run(ctx)
		if err == nil || !strings.Contains(err.Error(), "kaput") {
			t.Fatalf("want error containing kaput, got %v", err)
		}
		assertEqual(t, stub.capCalls, 1)
	})

	t.Run("errors when manager nil", func(t *testing.T) {
		agent := &miniedr.EDRAgent{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err := agent.Run(ctx)
		assertError(t, err, "edr agent: Manager is nil")
	})
}
