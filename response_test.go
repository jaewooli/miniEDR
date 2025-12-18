package miniedr

import (
	"errors"
	"testing"
	"time"
)

type stubResponder struct {
	err error
	n   int
}

func (s *stubResponder) Name() string { return "stub" }
func (s *stubResponder) Handle(alert Alert) error {
	s.n++
	return s.err
}

func TestPolicyEngine(t *testing.T) {
	p := &PolicyEngine{
		MinSeverity: SeverityHigh,
		AllowRules:  []string{"r1", "r2"},
		DenyRules:   []string{"r2"},
		Cooldown:    time.Minute,
	}
	a := Alert{RuleID: "r1", Severity: SeverityHigh}
	if !p.ShouldRespond(a) {
		t.Fatalf("expected allow")
	}
	a2 := Alert{RuleID: "r2", Severity: SeverityCritical}
	if p.ShouldRespond(a2) {
		t.Fatalf("expected deny by list")
	}
	a3 := Alert{RuleID: "r3", Severity: SeverityHigh}
	if p.ShouldRespond(a3) {
		t.Fatalf("expected filtered by allow list")
	}
	// cooldown
	a4 := Alert{RuleID: "r1", Severity: SeverityHigh, DedupKey: "k"}
	_ = p.ShouldRespond(a4)
	if p.ShouldRespond(a4) {
		t.Fatalf("expected cooldown to suppress second alert")
	}
}

func TestResponseRouter(t *testing.T) {
	resp := &stubResponder{}
	router := &ResponseRouter{
		Policy:   &PolicyEngine{MinSeverity: SeverityLow},
		Pipeline: &ResponderPipeline{Responders: []AlertResponder{resp}},
	}
	alerts := []Alert{
		{RuleID: "r1", Severity: SeverityInfo},
		{RuleID: "r2", Severity: SeverityHigh},
	}
	errs := router.Run(alerts)
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if resp.n != 1 {
		t.Fatalf("expected 1 alert handled, got %d", resp.n)
	}

	// pipeline nil error
	router.Pipeline = nil
	errs = router.Run([]Alert{{RuleID: "r3", Severity: SeverityHigh}})
	if len(errs) == 0 {
		t.Fatalf("expected error when pipeline nil")
	}
}

func TestResponderPipelineError(t *testing.T) {
	r := &ResponderPipeline{Responders: []AlertResponder{&stubResponder{err: errors.New("boom")}}}
	errs := r.Run([]Alert{{RuleID: "r", Severity: SeverityHigh}})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %v", errs)
	}
}
