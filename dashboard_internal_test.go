package miniedr

import (
	"strings"
	"testing"
)

func TestDeriveGraphCPUAndMem(t *testing.T) {
	g := deriveGraph("CPUCapturer", "CPUSnapshot(at=..., totalUsage=1.1%, cpu0=0.5%)")
	if g.Label != "CPU total" || g.Value != 1.1 || g.Display < 1 {
		t.Fatalf("unexpected cpu graph: %+v", g)
	}

	g = deriveGraph("MEMCapturer", "MEMSnapshot(at=..., RAM: Total=0B Avail=0B UsedApprox=0B (0.00%), Free=0B Buffers=0B Cached=0B; Swap: Total=0B Used=0B (0.00%) Free=0B, Sin=0B Sout=0B)")
	if g.Label != "RAM used" || g.Value != 0 {
		t.Fatalf("unexpected mem graph: %+v", g)
	}
	if g.Display != 0 {
		t.Fatalf("mem display should be zero when value zero, got %v", g.Display)
	}
}

func TestDeriveGraphDiskNet(t *testing.T) {
	d := deriveGraph("DISKCapturer", "DISKSnapshot(at=..., / used=50.00% (500/1000B), ioRate=read 0B/s write 0B/s, devices=1)")
	if d.Label != "DISK used" || d.Value != 50 {
		t.Fatalf("unexpected disk graph: %+v", d)
	}

	n := deriveGraph("NETCapturer", "NETSnapshot(at=..., ifaces=2, rxRate=1048576B/s, txRate=1048576B/s)")
	if !strings.Contains(n.Label, "NET") || n.Value == 0 {
		t.Fatalf("unexpected net graph: %+v", n)
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
		{1, 1},
		{50, 50},
		{120, 100},
	}
	for _, tt := range tests {
		got := clampGraphValue(tt.in)
		if got != tt.want {
			t.Fatalf("clampGraphValue(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
