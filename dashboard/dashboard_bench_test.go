package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jaewooli/miniedr"
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

func buildAlertEntries(n int, spanSeconds int) ([]dashboardAlertEntry, map[string]time.Duration) {
	entries := make([]dashboardAlertEntry, 0, n)
	intervals := map[string]time.Duration{
		"CPU": 2 * time.Second,
		"MEM": 2 * time.Second,
		"NET": 2 * time.Second,
	}
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	sources := []string{"CPU", "MEM", "NET"}
	if spanSeconds <= 0 {
		spanSeconds = 60
	}
	for i := 0; i < n; i++ {
		at := start.Add(time.Duration(i%spanSeconds) * time.Second)
		src := sources[i%len(sources)]
		entries = append(entries, dashboardAlertEntry{
			ID:     "a" + strconv.Itoa(i),
			AtTime: at,
			Source: src,
		})
	}
	return entries, intervals
}

func BenchmarkCorrelateAlerts200(b *testing.B) {
	entries, intervals := buildAlertEntries(200, 300)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = correlateAlerts(entries, intervals, 2*time.Second)
	}
}

func BenchmarkCorrelateAlerts1000(b *testing.B) {
	entries, intervals := buildAlertEntries(1000, 3000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = correlateAlerts(entries, intervals, 2*time.Second)
	}
}

type benchAlertCapturer struct {
	info capturer.InfoData
}

func (b *benchAlertCapturer) Capture() error { return nil }
func (b *benchAlertCapturer) GetInfo() (capturer.InfoData, error) {
	return b.info, nil
}

func BenchmarkAlertHistoryUpdate(b *testing.B) {
	info := capturer.InfoData{
		Summary: "bench",
		Meta:    capturer.TelemetryMeta{CapturedAt: time.Unix(100, 0)},
	}
	cap := &benchAlertCapturer{info: info}
	ds := NewDashboardServer(capturer.Capturers{cap}, "bench", false)
	ds.rulesPath = ""
	ds.metricsPath = ""
	ds.mu.Lock()
	ds.detector = &miniedr.Detector{
		Rules: []miniedr.RuleSpec{{
			ID:       "bench.alert",
			Title:    "Bench Alert",
			Severity: miniedr.SeverityInfo,
			Eval: func(info capturer.InfoData) []miniedr.Alert {
				return []miniedr.Alert{{
					RuleID:  "bench.alert",
					Title:   "Bench Alert",
					Message: "bench",
					At:      info.Meta.CapturedAt,
				}}
			},
		}},
	}
	ds.mu.Unlock()
	ref := time.Unix(200, 0).Format(time.RFC3339)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ds.captureSingle(cap, ref, "bench", false, false, 0, false, 0)
	}
}

func BenchmarkAlertHistoryToEvent(b *testing.B) {
	info := capturer.InfoData{
		Summary: "bench",
		Meta:    capturer.TelemetryMeta{CapturedAt: time.Unix(100, 0)},
	}
	cap := &benchAlertCapturer{info: info}
	ds := NewDashboardServer(capturer.Capturers{cap}, "bench", false)
	ds.rulesPath = ""
	ds.metricsPath = ""
	ds.mu.Lock()
	ds.detector = &miniedr.Detector{
		Rules: []miniedr.RuleSpec{{
			ID:       "bench.alert",
			Title:    "Bench Alert",
			Severity: miniedr.SeverityInfo,
			Eval: func(info capturer.InfoData) []miniedr.Alert {
				return []miniedr.Alert{{
					RuleID:  "bench.alert",
					Title:   "Bench Alert",
					Message: "bench",
					At:      info.Meta.CapturedAt,
				}}
			},
		}},
	}
	ch := make(chan string, 2)
	ds.clients[ch] = struct{}{}
	ds.mu.Unlock()
	ref := time.Unix(200, 0).Format(time.RFC3339)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for {
			select {
			case <-ch:
				continue
			default:
			}
			break
		}
		ds.captureSingle(cap, ref, "bench", false, false, 0, false, 0)
		<-ch
	}
}
