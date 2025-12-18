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
)

type stubEDRCapturer struct {
	captureErr error
	info       miniedr.InfoData
	infoErr    error
	capCalls   int
	infoCalls  int
}

func (s *stubEDRCapturer) Capture() error {
	s.capCalls++
	return s.captureErr
}

func (s *stubEDRCapturer) GetInfo() (miniedr.InfoData, error) {
	s.infoCalls++
	return s.info, s.infoErr
}

func TestEDRAgentRun(t *testing.T) {
	t.Run("runs until context done", func(t *testing.T) {
		buf := &bytes.Buffer{}
		stub := &stubEDRCapturer{info: miniedr.InfoData{Summary: "ok"}}
		edrAgent := agent.NewEDRAgent([]miniedr.CapturerSchedule{
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
		stub := &stubEDRCapturer{captureErr: errors.New("kaput"), info: miniedr.InfoData{Summary: "ok"}}
		edrAgent := agent.NewEDRAgent([]miniedr.CapturerSchedule{
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
		edrAgent := &agent.EDRAgent{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err := edrAgent.Run(ctx)
		assertError(t, err, "edr agent: schedules is empty")
	})
}
