package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jaewooli/miniedr/capturer"
)

func newBenchServer() *DashboardServer {
	d := NewDashboardServer(capturer.Capturers{}, "bench", false)
	d.rulesPath = ""
	d.metricsPath = ""
	d.defaultMetrics = []string{"cpu.total_pct", "mem.ram.used_pct"}
	return d
}

func BenchmarkHandleRulesGet(b *testing.B) {
	d := newBenchServer()
	req := httptest.NewRequest(http.MethodGet, "/rules", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		d.handleRules(w, req)
	}
}

func BenchmarkHandleRulesPost(b *testing.B) {
	d := newBenchServer()
	payload := RulesConfig{
		Rules: []RuleDefinition{
			{ID: "bench.rule", Title: "Bench", Severity: "info", Metric: "cpu.total_pct", Op: ">=", Value: 90, Enabled: true},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		b.Fatalf("marshal: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/rules", bytes.NewReader(body))
		w := httptest.NewRecorder()
		d.handleRules(w, req)
	}
}

func BenchmarkHandleMetricsGet(b *testing.B) {
	d := newBenchServer()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		d.handleMetrics(w, req)
	}
}

func BenchmarkHandleMetricsPost(b *testing.B) {
	d := newBenchServer()
	payload := metricsPayload{
		Custom: []CustomMetricPayload{{Name: "bench.metric", Expr: "cpu.total_pct"}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		b.Fatalf("marshal: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/metrics", bytes.NewReader(body))
		w := httptest.NewRecorder()
		d.handleMetrics(w, req)
	}
}
