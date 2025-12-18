package miniedr

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jaewooli/miniedr/capturer"
)

func TestDetectorEnrichesAndDedups(t *testing.T) {
	info := capturer.InfoData{
		Summary: "CPUSnapshot",
		Metrics: map[string]float64{"cpu.total_pct": 95},
		Meta: capturer.TelemetryMeta{
			Host:       "h",
			Session:    "s",
			Capturer:   "CPU",
			CapturedAt: time.Unix(1, 0),
		},
	}
	det := &Detector{
		Rules:   []Rule{RuleCPUHigh(90)},
		Deduper: &AlertDeduper{Window: time.Hour},
	}
	alerts := det.Evaluate(info)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	a := alerts[0]
	if a.Meta.Host != "h" || a.Meta.Capturer != "CPU" || a.RuleID != "cpu.high_usage" || a.ID == "" {
		t.Fatalf("alert not enriched: %+v", a)
	}
	// Second evaluate should dedup
	if out := det.Evaluate(info); len(out) != 0 {
		t.Fatalf("expected deduped alerts=0, got %d", len(out))
	}
}

func TestResponderPipeline(t *testing.T) {
	buf := &bytes.Buffer{}
	resp := &ResponderPipeline{Responders: []AlertResponder{&LogResponder{Out: buf}}}
	alert := Alert{
		ID:       "a1",
		RuleID:   "r1",
		Title:    "t1",
		Severity: SeverityHigh,
		Message:  "m",
		At:       time.Now(),
	}
	errs := resp.Run([]Alert{alert})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if !strings.Contains(buf.String(), `"rule_id":"r1"`) {
		t.Fatalf("log output missing rule id: %s", buf.String())
	}
}

func TestLimiter(t *testing.T) {
	lim := &RateLimiter{Window: time.Hour, Burst: 1}
	alerts := []Alert{
		{RuleID: "r", DedupKey: "k"},
		{RuleID: "r", DedupKey: "k"},
	}
	out := lim.Filter(alerts)
	if len(out) != 1 {
		t.Fatalf("expected 1 allowed, got %d", len(out))
	}
}
