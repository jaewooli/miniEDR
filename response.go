package miniedr

import (
	"fmt"
	"sync"
	"time"
)

// PolicyEngine decides whether an alert should trigger responders.
type PolicyEngine struct {
	MinSeverity AlertSeverity
	AllowRules  []string
	DenyRules   []string
	Cooldown    time.Duration // per alert dedup key

	mu   sync.Mutex
	last map[string]time.Time
}

// ShouldRespond returns true if the alert passes policy checks.
func (p *PolicyEngine) ShouldRespond(a Alert) bool {
	if p == nil {
		return true
	}
	if inList(a.RuleID, p.DenyRules) {
		return false
	}
	if len(p.AllowRules) > 0 && !inList(a.RuleID, p.AllowRules) {
		return false
	}
	if p.MinSeverity != "" && severityRank(a.Severity) < severityRank(p.MinSeverity) {
		return false
	}
	if p.Cooldown > 0 {
		key := a.DedupKey
		if key == "" {
			key = a.RuleID
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.last == nil {
			p.last = make(map[string]time.Time)
		}
		if until, ok := p.last[key]; ok && time.Now().Before(until) {
			return false
		}
		p.last[key] = time.Now().Add(p.Cooldown)
	}
	return true
}

// ResponseRouter filters alerts via policy then fans out to responders.
type ResponseRouter struct {
	Policy    *PolicyEngine
	Pipeline  *ResponderPipeline
	OnDropped func(Alert) // optional hook for dropped alerts
}

func (r *ResponseRouter) Run(alerts []Alert) []error {
	if r == nil || len(alerts) == 0 {
		return nil
	}
	var filtered []Alert
	for _, a := range alerts {
		if r.Policy == nil || r.Policy.ShouldRespond(a) {
			filtered = append(filtered, a)
		} else if r.OnDropped != nil {
			r.OnDropped(a)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if r.Pipeline == nil {
		return []error{fmt.Errorf("response router: pipeline is nil")}
	}
	return r.Pipeline.Run(filtered)
}

func inList(val string, list []string) bool {
	for _, v := range list {
		if v == val {
			return true
		}
	}
	return false
}

func severityRank(s AlertSeverity) int {
	switch s {
	case SeverityCritical:
		return 5
	case SeverityHigh:
		return 4
	case SeverityMedium:
		return 3
	case SeverityLow:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}
