package capturer

import (
	"reflect"
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
}

// CapturerName returns the underlying type name of a capturer, used for display/logging.
func CapturerName(c Capturer) string {
	t := reflect.TypeOf(c)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Name()
}
