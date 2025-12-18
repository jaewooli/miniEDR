package capturer

import (
	"reflect"
	"time"
)

// InfoData carries both human readable text and structured metrics to avoid downstream string parsing.
// Zero-value is usable; nil maps are treated as empty.
type InfoData struct {
	Summary string
	// Metrics holds numeric values keyed by dotted names (e.g. "cpu.total_pct").
	// Keeping it flat makes it easy for dashboards or alerts to consume without custom parsers.
	Metrics map[string]float64
	// Fields can carry non-numeric metadata if needed. Leave nil when unused.
	Fields map[string]interface{}
	// Meta carries standard telemetry context set by the runtime/agent.
	Meta TelemetryMeta
}

// TelemetryMeta standardizes host/session context for all captures.
type TelemetryMeta struct {
	Host         string    `json:"host,omitempty"`
	AgentVersion string    `json:"agent_version,omitempty"`
	AgentBuild   string    `json:"agent_build,omitempty"`
	Session      string    `json:"session,omitempty"`
	Timezone     string    `json:"timezone,omitempty"`
	CapturedAt   time.Time `json:"captured_at,omitempty"`
	OS           string    `json:"os,omitempty"`
	Arch         string    `json:"arch,omitempty"`
	Capturer     string    `json:"capturer,omitempty"`
	IntervalSec  float64   `json:"interval_sec,omitempty"`
	MaxFiles     int       `json:"max_files,omitempty"` // optional for file change
}

// CapturerName returns the underlying type name of a capturer, used for display/logging.
func CapturerName(c Capturer) string {
	t := reflect.TypeOf(c)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Name()
}
