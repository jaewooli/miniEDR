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
	captureErr  error
	captureErrs []error
	info        capturer.InfoData
	infoErr     error
	capCalls    int
	infoCalls   int
}

func (s *stubEDRCapturer) Capture() error {
	s.capCalls++
	if len(s.captureErrs) > 0 {
		err := s.captureErrs[0]
		s.captureErrs = s.captureErrs[1:]
		return err
	}
	return s.captureErr
}

func (s *stubEDRCapturer) GetInfo() (capturer.InfoData, error) {
	s.infoCalls++
	return s.info, s.infoErr
}

type stubSink struct {
	errs     []error
	consumed int
	last     capturer.InfoData
}

func (s *stubSink) Consume(info capturer.InfoData) error {
	s.last = info
	s.consumed++
	if len(s.errs) == 0 {
		return nil
	}
	err := s.errs[0]
	s.errs = s.errs[1:]
	return err
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

	t.Run("records errors but keeps running", func(t *testing.T) {
		buf := &bytes.Buffer{}
		stub := &stubEDRCapturer{
			captureErrs: []error{errors.New("kaput"), nil},
			info:        capturer.InfoData{Summary: "ok"},
		}
		edrAgent := agent.NewCollectAgent([]miniedr.CapturerSchedule{
			{Capturer: stub, Interval: 5 * time.Millisecond},
		})
		edrAgent.Out = buf
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
		defer cancel()

		err := edrAgent.Run(ctx)
		if err == nil || !strings.Contains(err.Error(), "kaput") {
			t.Fatalf("want first capture error returned, got %v", err)
		}
		if len(edrAgent.Errs) == 0 {
			t.Fatalf("expected errors recorded")
		}
		if stub.capCalls < 2 {
			t.Fatalf("expected captures to continue after error, got %d", stub.capCalls)
		}
		if stub.infoCalls < 1 {
			t.Fatalf("expected info to be read after error recovery")
		}
		if strings.Count(buf.String(), "ok") < 1 {
			t.Fatalf("expected output after successful capture, got %q", buf.String())
		}
	})

	t.Run("runs detection and responders", func(t *testing.T) {
		buf := &bytes.Buffer{}
		stub := &stubEDRCapturer{info: capturer.InfoData{
			Summary: "cpu",
			Metrics: map[string]float64{"cpu.total_pct": 95},
			Meta:    capturer.TelemetryMeta{Capturer: "CPU"},
		}}
		edrAgent := agent.NewCollectAgent([]miniedr.CapturerSchedule{
			{Capturer: stub, Interval: 5 * time.Millisecond},
		})
		edrAgent.Out = buf
		edrAgent.Pipeline = &miniedr.AlertPipeline{
			Detector: &miniedr.Detector{Rules: []miniedr.RuleSpec{miniedr.RuleCPUHigh(90)}},
		}
		logBuf := &bytes.Buffer{}
		edrAgent.AddResponder(&miniedr.LogResponder{Out: logBuf})
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Millisecond)
		defer cancel()

		_ = edrAgent.Run(ctx)

		if logBuf.Len() == 0 {
			t.Fatalf("expected responder to log alert")
		}
		if !strings.Contains(logBuf.String(), "cpu.high_usage") {
			t.Fatalf("expected rule id in log, got %q", logBuf.String())
		}
	})

	t.Run("records sink errors and stats", func(t *testing.T) {
		buf := &bytes.Buffer{}
		stub := &stubEDRCapturer{info: capturer.InfoData{Summary: "ok"}}
		sink := &stubSink{errs: []error{errors.New("sink kaput")}}
		edrAgent := agent.NewCollectAgent([]miniedr.CapturerSchedule{
			{Capturer: stub, Interval: 5 * time.Millisecond},
		})
		edrAgent.Out = buf
		edrAgent.AddSink(sink)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		err := edrAgent.Run(ctx)
		if err == nil || !strings.Contains(err.Error(), "sink kaput") {
			t.Fatalf("want sink error returned, got %v", err)
		}
		stats := edrAgent.SinkStats()
		ss, ok := stats["stubSink"]
		if !ok {
			t.Fatalf("expected sink stats present, got %+v", stats)
		}
		assertTrue(t, ss.Failure >= 1)
		assertTrue(t, ss.Success >= 1)
		if ss.LastErr != nil {
			t.Fatalf("expected last error cleared after success, got %v", ss.LastErr)
		}
		assertTrue(t, sink.consumed >= 2)
	})

	t.Run("fills telemetry meta", func(t *testing.T) {
		buf := &bytes.Buffer{}
		stub := &stubEDRCapturer{info: capturer.InfoData{Summary: "ok"}}
		sink := &stubSink{}
		edrAgent := agent.NewCollectAgent([]miniedr.CapturerSchedule{
			{Capturer: stub, Interval: 10 * time.Millisecond},
		})
		edrAgent.Out = buf
		edrAgent.AddSink(sink)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
		defer cancel()

		_ = edrAgent.Run(ctx)

		meta := sink.last.Meta
		if meta.Host == "" || meta.Session == "" || meta.AgentVersion == "" || meta.Capturer == "" {
			t.Fatalf("meta not populated: %+v", meta)
		}
		if meta.IntervalSec == 0 {
			t.Fatalf("expected interval meta set, got %+v", meta)
		}
		if meta.OS == "" || meta.Arch == "" {
			t.Fatalf("expected os/arch set, got %+v", meta)
		}
	})

	t.Run("returns capture error immediately", func(t *testing.T) {
		buf := &bytes.Buffer{}
		stub := &stubEDRCapturer{captureErr: errors.New("kaput"), info: capturer.InfoData{Summary: "ok"}}
		edrAgent := agent.NewCollectAgent([]miniedr.CapturerSchedule{
			{Capturer: stub, Interval: 5 * time.Millisecond},
		})
		edrAgent.Out = buf
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		err := edrAgent.Run(ctx)
		if err == nil || !strings.Contains(err.Error(), "kaput") {
			t.Fatalf("want error containing kaput, got %v", err)
		}
		if stub.capCalls < 1 {
			t.Fatalf("expected at least one capture attempt, got %d", stub.capCalls)
		}
	})

	t.Run("errors when manager nil", func(t *testing.T) {
		edrAgent := &agent.CollectAgent{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		err := edrAgent.Run(ctx)
		assertError(t, err, "edr agent: schedules is empty")
	})
}
