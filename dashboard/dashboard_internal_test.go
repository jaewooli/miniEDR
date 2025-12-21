package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jaewooli/miniedr"
	"github.com/jaewooli/miniedr/capturer"
)

func TestDeriveGraphVariants(t *testing.T) {
	cases := []struct {
		name   string
		graphs []graphInfo
		expect func([]graphInfo) bool
	}{
		{"cpu total", deriveGraphs("CPUCapturer", capturer.InfoData{Summary: "CPUSnapshot(at=..., totalUsage=1.1%, cpu0=0.5%)"}, 0, nil), func(g []graphInfo) bool { return len(g) == 1 && g[0].Label == "CPU avg" && g[0].Value == 1.1 }},
		{"mem zero", deriveGraphs("MEMCapturer", capturer.InfoData{Summary: "MEMSnapshot(at=..., RAM: Total=0B Avail=0B UsedApprox=0B (0.00%), Free=0B Buffers=0B Cached=0B; Swap: Total=0B Used=0B (0.00%) Free=0B, Sin=0B Sout=0B)"}, 0, nil), func(g []graphInfo) bool { return len(g) >= 1 && g[0].Label == "RAM used" && g[0].Value == 0 }},
		{"mem nonzero", deriveGraphs("MEMCapturer", capturer.InfoData{Summary: "MEMSnapshot(at=..., RAM: Total=100B Avail=10B UsedApprox=90B (26.49%), Free=0B Buffers=0B Cached=0B; Swap: Total=0B Used=0B (0.00%) Free=0B, Sin=0B Sout=0B)"}, 0, nil), func(g []graphInfo) bool { return len(g) >= 1 && g[0].Label == "RAM used" && g[0].Value > 26 }},
		{"mem with swap", deriveGraphs("MEMCapturer", capturer.InfoData{Summary: "MEMSnapshot(at=..., RAM: Total=100B Avail=10B UsedApprox=90B (26.49%), Free=0B Buffers=0B Cached=0B; Swap: Total=10B Used=5B (50.00%) Free=0B, Sin=0B Sout=0B)"}, 0, nil), func(g []graphInfo) bool { return len(g) == 2 && g[1].Label == "Swap used" && g[1].Value == 50 }},
		{"disk used", deriveGraphs("DISKCapturer", capturer.InfoData{Summary: "DISKSnapshot(at=..., / used=50.00% (500/1000B), ioRate=read 0B/s write 0B/s, devices=1)"}, 0, nil), func(g []graphInfo) bool { return len(g) == 1 && g[0].Label == "DISK used" && g[0].Value == 50 }},
		{"disk zero", deriveGraphs("DISKCapturer", capturer.InfoData{Summary: "DISKSnapshot(at=..., / used=0.00% (0/1000B), ioRate=read 0B/s write 0B/s, devices=1)"}, 0, nil), func(g []graphInfo) bool { return len(g) == 1 && g[0].Value == 0 }},
		{"net rate", deriveGraphs("NETCapturer", capturer.InfoData{Summary: "NETSnapshot(at=..., ifaces=2, rxRate=1048576B/s, txRate=1048576B/s)"}, 0, nil), func(g []graphInfo) bool { return len(g) == 1 && strings.HasPrefix(g[0].Label, "NET") && g[0].Value > 0 }},
		{"net zero", deriveGraphs("NETCapturer", capturer.InfoData{Summary: "NETSnapshot(at=..., ifaces=2, rxRate=0B/s, txRate=0B/s)"}, 0, nil), func(g []graphInfo) bool { return len(g) == 1 && strings.HasPrefix(g[0].Label, "NET") }},
		{"unknown capturer", deriveGraphs("FooCapturer", capturer.InfoData{Summary: "random text"}, 0, nil), func(g []graphInfo) bool { return len(g) == 0 }},
		{"file change events", deriveGraphs("FileChangeCapturer", capturer.InfoData{Summary: "FileChangeSnapshot(at=..., files=3, events=5, sample=created:a.txt)"}, 0, nil), func(g []graphInfo) bool {
			return len(g) == 1 && g[0].Label == "File events" && g[0].Value == 5
		}},
	}

	for _, tt := range cases {
		if !tt.expect(tt.graphs) {
			t.Fatalf("%s failed: %+v", tt.name, tt.graphs)
		}
	}
}

func TestClampGraphValue(t *testing.T) {
	tests := []struct {
		in   float64
		want float64
	}{
		{-1, 0},
		{0, 0},
		{0.1, 1},
		{0.9, 1},
		{1, 1},
		{10, 10},
		{50, 50},
		{75.5, 75.5},
		{99.9, 99.9},
		{120, 100},
	}
	for _, tt := range tests {
		got := clampGraphValue(tt.in)
		if got != tt.want {
			t.Fatalf("clampGraphValue(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestSummariesTable(t *testing.T) {
	tests := []struct {
		name string
		in   capturer.InfoData
		want []string
	}{
		{"cpu full", capturer.InfoData{Summary: "CPUSnapshot(at=..., totalUsage=10.00%, cpu0=5.0% cpu1=15.0%)", Metrics: map[string]float64{"cpu.core0_pct": 5, "cpu.core1_pct": 15, "cpu.total_pct": 10}}, []string{"Avg 10.00%", "cpu0=5.0%", "cpu1=15.0%"}},
		{"cpu avg only", capturer.InfoData{Summary: "CPUSnapshot(at=..., totalUsage=5.00%, cpu0=2.0%)"}, []string{"Avg 5.00%"}},
		{"mem percent total", capturer.InfoData{Summary: "MEMSnapshot(at=..., RAM: Total=1024B Avail=512B UsedApprox=512B (26.49%), Free=0B Buffers=0B Cached=0B; Swap: Total=0B Used=0B (0.00%) Free=0B, Sin=0B Sout=0B)"}, []string{"RAM 26.49%", "Total 1.0KB"}},
		{"mem swap", capturer.InfoData{Summary: "MEMSnapshot(at=..., RAM: Total=2048B Avail=1024B UsedApprox=1024B (50.00%), Free=0B Buffers=0B Cached=0B; Swap: Total=1024B Used=256B (25.00%) Free=0B, Sin=0B Sout=0B)"}, []string{"RAM 50.00%", "Swap 256B (25.00%)"}},
		{"disk io", capturer.InfoData{Summary: "DISKSnapshot(at=..., / used=75.00% (750/1000B), ioRate=read 10B/s write 20B/s, devices=1)"}, []string{"Used 75.00%", "read 10B/s write 20B/s"}},
		{"disk zero", capturer.InfoData{Summary: "DISKSnapshot(at=..., / used=0.00% (0/1000B), ioRate=n/a, devices=1)"}, []string{"Used 0.00%"}},
		{"net both", capturer.InfoData{Summary: "NETSnapshot(at=..., ifaces=2, rxRate=1024B/s, txRate=2048B/s)"}, []string{"RX 1024B/s", "TX 2048B/s"}},
		{"net rx only", capturer.InfoData{Summary: "NETSnapshot(at=..., ifaces=2, rxRate=512B/s, txRate=n/a)"}, []string{"RX 512B/s"}},
		{"unknown", capturer.InfoData{Summary: "plain text here"}, []string{"plain text here"}},
	}

	for _, tt := range tests {
		sum := summarizeInfo(tt.name+"Capturer", tt.in)
		for _, want := range tt.want {
			if !strings.Contains(sum, want) {
				t.Fatalf("%s summary missing %q: %s", tt.name, want, sum)
			}
		}
	}
}

func TestChartXLargeTotalSpreadsToEdges(t *testing.T) {
	total := 500
	if got := chartX(0, total); got != 0 {
		t.Fatalf("chartX first want 0, got %d", got)
	}
	if got := chartX(total-1, total); got != 220 {
		t.Fatalf("chartX last want 220, got %d", got)
	}
	mid := chartX(total/2, total)
	if mid <= 0 || mid >= 220 {
		t.Fatalf("chartX mid should be between edges, got %d", mid)
	}
}

type alertStub struct {
	infos []capturer.InfoData
	idx   int
}

func (a *alertStub) Capture() error {
	if a.idx < 0 {
		a.idx = 0
		return nil
	}
	if a.idx < len(a.infos)-1 {
		a.idx++
	}
	return nil
}

func (a *alertStub) GetInfo() (capturer.InfoData, error) {
	if a.idx < 0 || len(a.infos) == 0 {
		return capturer.InfoData{}, nil
	}
	return a.infos[a.idx], nil
}

func TestRulesHandlerRoundTrip(t *testing.T) {
	ds := NewDashboardServer(capturer.Capturers{}, "TestDash", false)
	ds.rulesPath = ""
	ds.rulesConfig = defaultRulesConfig()

	req := httptest.NewRequest(http.MethodGet, "/rules", nil)
	w := httptest.NewRecorder()
	ds.handleRules(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /rules status = %d", w.Code)
	}
	var got RulesConfig
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET /rules: %v", err)
	}
	want := defaultRulesConfig()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GET /rules = %+v, want %+v", got, want)
	}

	update := RulesConfig{
		Rules: []RuleDefinition{{
			ID:       "custom.test",
			Title:    "Custom Test",
			Severity: miniedr.SeverityHigh,
			Metric:   "cpu.total_pct",
			Op:       ">=",
			Value:    42,
			Message:  "CPU {value}% >= {threshold}%",
			Enabled:  true,
		}},
	}
	body, _ := json.Marshal(update)
	req = httptest.NewRequest(http.MethodPost, "/rules", bytes.NewReader(body))
	w = httptest.NewRecorder()
	ds.handleRules(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /rules status = %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/rules", nil)
	w = httptest.NewRecorder()
	ds.handleRules(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /rules after update status = %d", w.Code)
	}
	var gotAfter RulesConfig
	if err := json.NewDecoder(w.Body).Decode(&gotAfter); err != nil {
		t.Fatalf("decode GET /rules after update: %v", err)
	}
	if !reflect.DeepEqual(gotAfter, update) {
		t.Fatalf("GET /rules after update = %+v, want %+v", gotAfter, update)
	}
}

func TestMetricsHandlerRoundTrip(t *testing.T) {
	ds := NewDashboardServer(capturer.Capturers{}, "TestDash", false)
	tmpDir := t.TempDir()
	ds.metricsPath = filepath.Join(tmpDir, "metrics.json")

	initial := []string{"cpu.total_pct", " net.rx_bytes_per_sec ", "cpu.total_pct", ""}
	data, err := json.MarshalIndent(initial, "", "  ")
	if err != nil {
		t.Fatalf("marshal initial metrics: %v", err)
	}
	if err := os.WriteFile(ds.metricsPath, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write metrics.json: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	ds.handleMetrics(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d", w.Code)
	}
	var got metricsPayload
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode GET /metrics: %v", err)
	}
	wantCustom := []string{"cpu.total_pct", "net.rx_bytes_per_sec"}
	if !reflect.DeepEqual(got.Custom, wantCustom) {
		t.Fatalf("GET /metrics custom = %+v, want %+v", got.Custom, wantCustom)
	}
	if len(got.All) != len(got.Custom) {
		t.Fatalf("GET /metrics all = %+v, want %+v", got.All, got.Custom)
	}

	update := []string{"mem.ram.used_pct", "cpu.total_pct"}
	body, _ := json.Marshal(update)
	req = httptest.NewRequest(http.MethodPost, "/metrics", bytes.NewReader(body))
	w = httptest.NewRecorder()
	ds.handleMetrics(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /metrics status = %d", w.Code)
	}
	var gotAfter metricsPayload
	if err := json.NewDecoder(w.Body).Decode(&gotAfter); err != nil {
		t.Fatalf("decode POST /metrics: %v", err)
	}
	wantAfter := []string{"cpu.total_pct", "mem.ram.used_pct"}
	if !reflect.DeepEqual(gotAfter.Custom, wantAfter) {
		t.Fatalf("POST /metrics custom = %+v, want %+v", gotAfter.Custom, wantAfter)
	}

	disk, err := os.ReadFile(ds.metricsPath)
	if err != nil {
		t.Fatalf("read metrics.json: %v", err)
	}
	var onDisk []string
	if err := json.Unmarshal(disk, &onDisk); err != nil {
		t.Fatalf("decode metrics.json: %v", err)
	}
	if !reflect.DeepEqual(onDisk, wantAfter) {
		t.Fatalf("metrics.json = %+v, want %+v", onDisk, wantAfter)
	}
}

func TestAlertHistoryTracksSnapshots(t *testing.T) {
	info1 := capturer.InfoData{
		Summary: "cpu alert 1",
		Metrics: map[string]float64{"cpu.total_pct": 95},
		Meta:    capturer.TelemetryMeta{CapturedAt: time.Unix(100, 0)},
	}
	info2 := capturer.InfoData{
		Summary: "cpu alert 2",
		Metrics: map[string]float64{"cpu.total_pct": 96},
		Meta:    capturer.TelemetryMeta{CapturedAt: time.Unix(101, 0)},
	}
	ds := NewDashboardServer(capturer.Capturers{&alertStub{infos: []capturer.InfoData{info1, info2}, idx: -1}}, "TestDash", false)

	ds.CaptureNow()
	ds.CaptureNow()

	snap := ds.currentSnapshot()
	if len(snap.GlobalAlerts) != 2 {
		t.Fatalf("GlobalAlerts len = %d, want 2", len(snap.GlobalAlerts))
	}
	last := snap.GlobalAlerts[len(snap.GlobalAlerts)-1]
	if last.Title != "High CPU usage" {
		t.Fatalf("last alert title = %q, want %q", last.Title, "High CPU usage")
	}
	if last.RuleID != "cpu.high_usage" {
		t.Fatalf("last alert rule = %q, want %q", last.RuleID, "cpu.high_usage")
	}
}
