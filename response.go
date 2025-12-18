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

	mu       sync.Mutex
	last     map[string]time.Time
	allowSet map[string]struct{}
	denySet  map[string]struct{}
}

// ShouldRespond returns true if the alert passes policy checks.
func (p *PolicyEngine) ShouldRespond(a Alert) bool {
	if p == nil {
		return true
	}
	if p.initSets(); p.inSet(a.RuleID, p.denySet) {
		return false
	}
	if len(p.allowSet) > 0 && !p.inSet(a.RuleID, p.allowSet) {
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

func (p *PolicyEngine) initSets() bool {
	if p.allowSet != nil || p.denySet != nil {
		return true
	}
	if len(p.AllowRules) > 0 {
		p.allowSet = make(map[string]struct{}, len(p.AllowRules))
		for _, r := range p.AllowRules {
			p.allowSet[r] = struct{}{}
		}
	}
	if len(p.DenyRules) > 0 {
		p.denySet = make(map[string]struct{}, len(p.DenyRules))
		for _, r := range p.DenyRules {
			p.denySet[r] = struct{}{}
		}
	}
	return true
}

func (p *PolicyEngine) inSet(val string, set map[string]struct{}) bool {
	if set == nil {
		return false
	}
	_, ok := set[val]
	return ok
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
