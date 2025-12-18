package miniedr_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jaewooli/miniedr"
	"github.com/jaewooli/miniedr/agent"
	"github.com/jaewooli/miniedr/capturer"
)

type stubEDRCapturer struct {
	captureErr error
	info       capturer.InfoData
	infoErr    error
	capCalls   int
	infoCalls  int
}

func (s *stubEDRCapturer) Capture() error {
	s.capCalls++
	return s.captureErr
}

func (s *stubEDRCapturer) GetInfo() (capturer.InfoData, error) {
	s.infoCalls++
	return s.info, s.infoErr
}

func assertTrue(t testing.TB, got bool) {
	t.Helper()
	if !got {
		t.Fatalf("expected true, got false")
	}
}

func assertEqual[T comparable](t testing.TB, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func assertError(t testing.TB, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil || err.Error() != want {
		t.Fatalf("want error %q, got %v", want, err)
	}
}

func TestEDRAgentRun(t *testing.T) {
	t.Run("runs until context done", func(t *testing.T) {
		buf := &bytes.Buffer{}
		stub := &stubEDRCapturer{info: capturer.InfoData{Summary: "ok"}}
		edrAgent := agent.NewCollectAgent([]miniedr.CapturerSchedule{
			{Capturer: stub, Interval: 5 * time.Millisecond},
		})
		edrAgent.Out = buf
		edrAgent.Verbose = true
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()

		err := edrAgent.Run(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("want context deadline error, got %v", err)
		}

		assertTrue(t, stub.capCalls >= 2)
		assertTrue(t, stub.infoCalls >= 2)
		assertTrue(t, strings.Count(buf.String(), "ok") >= 2)
	})

	t.Run("returns capture error immediately", func(t *testing.T) {
		buf := &bytes.Buffer{}
		stub := &stubEDRCapturer{captureErr: errors.New("kaput"), info: capturer.InfoData{Summary: "ok"}}
		edrAgent := agent.NewCollectAgent([]miniedr.CapturerSchedule{
			{Capturer: stub, Interval: 5 * time.Millisecond},
		})
		edrAgent.Out = buf
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err := edrAgent.Run(ctx)
		if err == nil || !strings.Contains(err.Error(), "kaput") {
			t.Fatalf("want error containing kaput, got %v", err)
		}
		assertEqual(t, stub.capCalls, 1)
	})

	t.Run("errors when manager nil", func(t *testing.T) {
		edrAgent := &agent.CollectAgent{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err := edrAgent.Run(ctx)
		assertError(t, err, "edr agent: schedules is empty")
	})
}
