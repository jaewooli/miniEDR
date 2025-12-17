package miniedr

import (
	"strings"
	"testing"
)

func TestDeriveGraphVariants(t *testing.T) {
	cases := []struct {
		name   string
		graphs []graphInfo
		expect func([]graphInfo) bool
	}{
		{"cpu total", deriveGraphs("CPUCapturer", "CPUSnapshot(at=..., totalUsage=1.1%, cpu0=0.5%)"), func(g []graphInfo) bool { return len(g) == 1 && g[0].Label == "CPU avg" && g[0].Value == 1.1 }},
		{"mem zero", deriveGraphs("MEMCapturer", "MEMSnapshot(at=..., RAM: Total=0B Avail=0B UsedApprox=0B (0.00%), Free=0B Buffers=0B Cached=0B; Swap: Total=0B Used=0B (0.00%) Free=0B, Sin=0B Sout=0B)"), func(g []graphInfo) bool { return len(g) >= 1 && g[0].Label == "RAM used" && g[0].Value == 0 }},
		{"mem nonzero", deriveGraphs("MEMCapturer", "MEMSnapshot(at=..., RAM: Total=100B Avail=10B UsedApprox=90B (26.49%), Free=0B Buffers=0B Cached=0B; Swap: Total=0B Used=0B (0.00%) Free=0B, Sin=0B Sout=0B)"), func(g []graphInfo) bool { return len(g) >= 1 && g[0].Label == "RAM used" && g[0].Value > 26 }},
		{"mem with swap", deriveGraphs("MEMCapturer", "MEMSnapshot(at=..., RAM: Total=100B Avail=10B UsedApprox=90B (26.49%), Free=0B Buffers=0B Cached=0B; Swap: Total=10B Used=5B (50.00%) Free=0B, Sin=0B Sout=0B)"), func(g []graphInfo) bool { return len(g) == 2 && g[1].Label == "Swap used" && g[1].Value == 50 }},
		{"disk used", deriveGraphs("DISKCapturer", "DISKSnapshot(at=..., / used=50.00% (500/1000B), ioRate=read 0B/s write 0B/s, devices=1)"), func(g []graphInfo) bool { return len(g) == 1 && g[0].Label == "DISK used" && g[0].Value == 50 }},
		{"disk zero", deriveGraphs("DISKCapturer", "DISKSnapshot(at=..., / used=0.00% (0/1000B), ioRate=read 0B/s write 0B/s, devices=1)"), func(g []graphInfo) bool { return len(g) == 1 && g[0].Value == 0 }},
		{"net rate", deriveGraphs("NETCapturer", "NETSnapshot(at=..., ifaces=2, rxRate=1048576B/s, txRate=1048576B/s)"), func(g []graphInfo) bool { return len(g) == 1 && strings.HasPrefix(g[0].Label, "NET") && g[0].Value > 0 }},
		{"net zero", deriveGraphs("NETCapturer", "NETSnapshot(at=..., ifaces=2, rxRate=0B/s, txRate=0B/s)"), func(g []graphInfo) bool { return len(g) == 1 && strings.HasPrefix(g[0].Label, "NET") }},
		{"unknown capturer", deriveGraphs("FooCapturer", "random text"), func(g []graphInfo) bool { return len(g) == 0 }},
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
		in   string
		want []string
	}{
		{"cpu full", "CPUSnapshot(at=..., totalUsage=10.00%, cpu0=5.0% cpu1=15.0%)", []string{"Avg 10.00%"}},
		{"cpu avg only", "CPUSnapshot(at=..., totalUsage=5.00%, cpu0=2.0%)", []string{"Avg 5.00%"}},
		{"mem percent total", "MEMSnapshot(at=..., RAM: Total=1024B Avail=512B UsedApprox=512B (26.49%), Free=0B Buffers=0B Cached=0B; Swap: Total=0B Used=0B (0.00%) Free=0B, Sin=0B Sout=0B)", []string{"RAM 26.49%", "Total 1.0KB"}},
		{"mem swap", "MEMSnapshot(at=..., RAM: Total=2048B Avail=1024B UsedApprox=1024B (50.00%), Free=0B Buffers=0B Cached=0B; Swap: Total=1024B Used=256B (25.00%) Free=0B, Sin=0B Sout=0B)", []string{"RAM 50.00%", "Swap 256B (25.00%)"}},
		{"disk io", "DISKSnapshot(at=..., / used=75.00% (750/1000B), ioRate=read 10B/s write 20B/s, devices=1)", []string{"Used 75.00%", "read 10B/s write 20B/s"}},
		{"disk zero", "DISKSnapshot(at=..., / used=0.00% (0/1000B), ioRate=n/a, devices=1)", []string{"Used 0.00%"}},
		{"net both", "NETSnapshot(at=..., ifaces=2, rxRate=1024B/s, txRate=2048B/s)", []string{"RX 1024B/s", "TX 2048B/s"}},
		{"net rx only", "NETSnapshot(at=..., ifaces=2, rxRate=512B/s, txRate=n/a)", []string{"RX 512B/s"}},
		{"unknown", "plain text here", []string{"plain text here"}},
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
