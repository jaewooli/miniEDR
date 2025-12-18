package miniedr

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/jaewooli/miniedr/capturer"
)

// AlertSeverity defines the urgency of a detection.
type AlertSeverity string

const (
	SeverityInfo     AlertSeverity = "info"
	SeverityLow      AlertSeverity = "low"
	SeverityMedium   AlertSeverity = "medium"
	SeverityHigh     AlertSeverity = "high"
	SeverityCritical AlertSeverity = "critical"
)

// Alert is a normalized detection event emitted by rules.
type Alert struct {
	ID          string                 `json:"id"`
	RuleID      string                 `json:"rule_id"`
	Title       string                 `json:"title"`
	Severity    AlertSeverity          `json:"severity"`
	Message     string                 `json:"message"`
	Source      string                 `json:"source"` // capturer name
	At          time.Time              `json:"at"`
	Meta        capturer.TelemetryMeta `json:"meta"`
	Evidence    map[string]any         `json:"evidence,omitempty"`
	Correlated  []string               `json:"correlated,omitempty"`
	DedupKey    string                 `json:"dedup_key,omitempty"`
	RateLimited bool                   `json:"rate_limited,omitempty"`
}

// RuleSpec produces zero or more alerts from a single capture, with metadata.
type RuleSpec struct {
	ID          string
	Title       string
	Description string
	Severity    AlertSeverity
	Tags        []string
	Source      string // optional capturer name this rule targets
	DedupKey    string // optional fixed dedup key
	Eval        func(info capturer.InfoData) []Alert
}

// Detector runs rules, enriches alerts with meta, and applies deduplication.
type Detector struct {
	Rules    []RuleSpec
	Deduper  *AlertDeduper
	Limiter  *RateLimiter
	IDSource func() string
}

// Evaluate runs all rules and returns alerts after enrichment and filtering.
func (d *Detector) Evaluate(info capturer.InfoData) []Alert {
	if d == nil {
		return nil
	}
	var out []Alert
	for _, rule := range d.Rules {
		if rule.Eval == nil {
			continue
		}
		alerts := rule.Eval(info)
		for i := range alerts {
			out = append(out, d.enrichAlert(alerts[i], info, rule))
		}
	}
	out = d.dedup(out)
	out = d.rateLimit(out)
	return out
}

func (d *Detector) enrichAlert(a Alert, info capturer.InfoData, r RuleSpec) Alert {
	if a.RuleID == "" {
		if r.ID != "" {
			a.RuleID = r.ID
		} else {
			a.RuleID = "unknown_rule"
		}
	}
	if a.ID == "" {
		if d.IDSource != nil {
			a.ID = d.IDSource()
		} else {
			a.ID = fmt.Sprintf("%s-%d", a.RuleID, time.Now().UnixNano())
		}
	}
	if a.At.IsZero() {
		if !info.Meta.CapturedAt.IsZero() {
			a.At = info.Meta.CapturedAt
		} else {
			a.At = time.Now()
		}
	}
	a.Meta = mergeMeta(a.Meta, info.Meta)
	if a.Source == "" {
		a.Source = a.Meta.Capturer
		if a.Source == "" {
			a.Source = "unknown"
		}
	}
	if a.Severity == "" {
		if r.Severity != "" {
			a.Severity = r.Severity
		} else {
			a.Severity = SeverityInfo
		}
	}
	if a.Title == "" {
		if r.Title != "" {
			a.Title = r.Title
		} else {
			a.Title = a.RuleID
		}
	}
	if a.Source == "" && r.Source != "" {
		a.Source = r.Source
	}
	if a.Evidence == nil && len(r.Tags) > 0 {
		a.Evidence = map[string]any{"tags": r.Tags}
	}
	if a.DedupKey == "" {
		if r.DedupKey != "" {
			a.DedupKey = r.DedupKey
		} else {
			a.DedupKey = fmt.Sprintf("%s|%s|%s", a.RuleID, a.Source, a.Meta.Session)
		}
	}
	return a
}

func (d *Detector) dedup(alerts []Alert) []Alert {
	if d == nil || d.Deduper == nil {
		return alerts
	}
	return d.Deduper.Filter(alerts)
}

func (d *Detector) rateLimit(alerts []Alert) []Alert {
	if d == nil || d.Limiter == nil {
		return alerts
	}
	return d.Limiter.Filter(alerts)
}

// AlertDeduper suppresses repeated alerts with the same key within a window.
type AlertDeduper struct {
	Window time.Duration

	mu   sync.Mutex
	seen map[string]time.Time
}

// Filter returns alerts that are not duplicates within the window.
func (d *AlertDeduper) Filter(alerts []Alert) []Alert {
	if d == nil || d.Window <= 0 {
		return alerts
	}
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen == nil {
		d.seen = make(map[string]time.Time)
	}
	var out []Alert
	for _, a := range alerts {
		key := a.DedupKey
		if key == "" {
			key = a.RuleID
		}
		if exp, ok := d.seen[key]; ok && now.Before(exp) {
			continue
		}
		out = append(out, a)
		d.seen[key] = now.Add(d.Window)
	}
	return out
}

// RateLimiter drops alerts when exceeded per key.
type RateLimiter struct {
	Window time.Duration
	Burst  int

	mu    sync.Mutex
	count map[string]int
	reset map[string]time.Time
}

// Filter returns alerts that are within rate limits; rate-limited alerts are dropped but flagged.
func (r *RateLimiter) Filter(alerts []Alert) []Alert {
	if r == nil || r.Window <= 0 || r.Burst <= 0 {
		return alerts
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count == nil {
		r.count = make(map[string]int)
		r.reset = make(map[string]time.Time)
	}
	var out []Alert
	for _, a := range alerts {
		key := a.DedupKey
		if key == "" {
			key = a.RuleID
		}
		resetAt, ok := r.reset[key]
		if !ok || now.After(resetAt) {
			r.count[key] = 0
			r.reset[key] = now.Add(r.Window)
		}
		if r.count[key] >= r.Burst {
			a.RateLimited = true
			continue
		}
		r.count[key]++
		out = append(out, a)
	}
	return out
}

// AlertResponder performs an action for an alert.
type AlertResponder interface {
	Name() string
	Handle(alert Alert) error
}

// ResponderPipeline fans out alerts to responders and collects errors.
type ResponderPipeline struct {
	Responders []AlertResponder
}

func (p *ResponderPipeline) Run(alerts []Alert) []error {
	if p == nil || len(p.Responders) == 0 || len(alerts) == 0 {
		return nil
	}
	var errs []error
	for _, a := range alerts {
		for _, r := range p.Responders {
			if r == nil {
				continue
			}
			if err := r.Handle(a); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", r.Name(), err))
			}
		}
	}
	return errs
}

// LogResponder writes alerts as JSON lines to an io.Writer.
type LogResponder struct {
	Out io.Writer
}

func (r *LogResponder) Name() string { return "log" }

func (r *LogResponder) Handle(alert Alert) error {
	if r.Out == nil {
		return fmt.Errorf("log responder: writer is nil")
	}
	enc := json.NewEncoder(r.Out)
	return enc.Encode(alert)
}

func mergeMeta(a, b capturer.TelemetryMeta) capturer.TelemetryMeta {
	out := a
	if out.Host == "" {
		out.Host = b.Host
	}
	if out.AgentVersion == "" {
		out.AgentVersion = b.AgentVersion
	}
	if out.AgentBuild == "" {
		out.AgentBuild = b.AgentBuild
	}
	if out.Session == "" {
		out.Session = b.Session
	}
	if out.Timezone == "" {
		out.Timezone = b.Timezone
	}
	if out.CapturedAt.IsZero() {
		out.CapturedAt = b.CapturedAt
	}
	if out.OS == "" {
		out.OS = b.OS
	}
	if out.Arch == "" {
		out.Arch = b.Arch
	}
	if out.Capturer == "" {
		out.Capturer = b.Capturer
	}
	if out.IntervalSec == 0 {
		out.IntervalSec = b.IntervalSec
	}
	if out.MaxFiles == 0 {
		out.MaxFiles = b.MaxFiles
	}
	return out
}
