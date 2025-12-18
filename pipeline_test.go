package miniedr

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jaewooli/miniedr/capturer"
)

func TestAlertPipelineProcess(t *testing.T) {
	det := &Detector{Rules: []RuleSpec{RuleCPUHigh(80)}}
	buf := &bytes.Buffer{}
	resp := &ResponderPipeline{Responders: []AlertResponder{&LogResponder{Out: buf}}}
	p := &AlertPipeline{Detector: det, Responder: resp}

	alerts, errs := p.Process(capturer.InfoData{
		Summary: "cpu",
		Metrics: map[string]float64{"cpu.total_pct": 90},
		Meta:    capturer.TelemetryMeta{Capturer: "CPU"},
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if !strings.Contains(buf.String(), "cpu.high_usage") {
		t.Fatalf("expected alert logged, got %q", buf.String())
	}
}

func TestAlertPipelineMissingResponder(t *testing.T) {
	det := &Detector{Rules: []RuleSpec{RuleCPUHigh(80)}}
	p := &AlertPipeline{Detector: det}
	alerts, errs := p.Process(capturer.InfoData{
		Metrics: map[string]float64{"cpu.total_pct": 90},
		Meta:    capturer.TelemetryMeta{Capturer: "CPU"},
	})
	if len(alerts) != 1 {
		t.Fatalf("expected alert emitted")
	}
	if len(errs) != 0 {
		t.Fatalf("expected no errors when no responders, got %v", errs)
	}
}
