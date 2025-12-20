package capturer

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/mem"
)

const memSnapshotText = "MEMSnapshot(at=%s, RAM: Total=%dB Avail=%dB UsedApprox=%dB (%.2f%%), Free=%dB Buffers=%dB Cached=%dB; Swap: Total=%dB Used=%dB (%.2f%%) Free=%dB, Sin=%dB Sout=%dB)"

type MEMSnapshot struct {
	At      time.Time
	Virtual *mem.VirtualMemoryStat
	Swap    *mem.SwapMemoryStat
}

type MEMCapturer struct {
	Now       func() time.Time
	VirtualFn func() (*mem.VirtualMemoryStat, error)
	SwapFn    func() (*mem.SwapMemoryStat, error)

	snapshot MEMSnapshot
	prev     *MEMSnapshot
}

func NewMEMCapturer() *MEMCapturer {
	return &MEMCapturer{
		Now:       time.Now,
		VirtualFn: mem.VirtualMemory,
		SwapFn:    mem.SwapMemory,
	}
}

func (m *MEMCapturer) Capture() error {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}

	if m.VirtualFn == nil {
		return errors.New("mem capturer: VirtualFn is nil")
	}
	if m.SwapFn == nil {
		return errors.New("mem capturer: SwapFn is nil")
	}

	v, err := m.VirtualFn()
	if err != nil {
		return err
	}
	s, err := m.SwapFn()
	if err != nil {
		return err
	}

	if m.snapshot.Virtual != nil && m.snapshot.Swap != nil {
		prev := m.snapshot
		m.prev = &prev
	}
	m.snapshot = MEMSnapshot{
		At:      now(),
		Virtual: v,
		Swap:    s,
	}
	return nil
}

func (m *MEMCapturer) GetInfo() (InfoData, error) {
	if m.snapshot.Virtual == nil || m.snapshot.Swap == nil {
		return InfoData{Summary: "MEMSnapshot(empty)"}, nil
	}

	v := m.snapshot.Virtual
	s := m.snapshot.Swap

	used := v.Total - v.Available
	usedPct := 0.0
	if v.Total > 0 {
		usedPct = float64(used) / float64(v.Total) * 100
	}
	swapPct := 0.0
	if s.Total > 0 {
		swapPct = float64(s.Used) / float64(s.Total) * 100
	}

	summary := fmt.Sprintf(
		memSnapshotText,
		m.snapshot.At.Format(time.RFC3339),
		v.Total, v.Available, used, usedPct,
		v.Free, v.Buffers, v.Cached,
		s.Total, s.Used, swapPct,
		s.Free, s.Sin, s.Sout,
	)

	metrics := map[string]float64{
		"mem.ram.total_bytes":  float64(v.Total),
		"mem.ram.used_bytes":   float64(used),
		"mem.ram.used_pct":     usedPct,
		"mem.swap.total_bytes": float64(s.Total),
		"mem.swap.used_bytes":  float64(s.Used),
		"mem.swap.used_pct":    swapPct,
	}

	return InfoData{Summary: summary, Metrics: metrics}, nil
}

// GetVerboseInfo returns a richer breakdown of RAM/swap fields.
func (m *MEMCapturer) GetVerboseInfo() (string, error) {
	if m.snapshot.Virtual == nil || m.snapshot.Swap == nil {
		return "MEMSnapshot(verbose-empty)", nil
	}

	v := m.snapshot.Virtual
	s := m.snapshot.Swap

	var b strings.Builder
	fmt.Fprintf(&b, "MEMSnapshot(at=%s", m.snapshot.At.Format(time.RFC3339))
	if m.prev != nil {
		fmt.Fprintf(&b, ", interval=%s", m.snapshot.At.Sub(m.prev.At).Round(time.Millisecond))
	}
	fmt.Fprintf(&b, ")\n")
	fmt.Fprintf(&b, "RAM: total=%dB avail=%dB used=%dB used%%=%.2f free=%dB active=%dB inactive=%dB wired=%dB buffers=%dB cached=%dB swapCached=%dB dirty=%dB writeback=%dB slab=%dB sreclaimable=%dB sunreclaim=%dB pageTables=%dB shared=%dB\n",
		v.Total, v.Available, v.Used, v.UsedPercent, v.Free,
		v.Active, v.Inactive, v.Wired, v.Buffers, v.Cached, v.SwapCached,
		v.Dirty, v.WriteBack, v.Slab, v.Sreclaimable, v.Sunreclaim, v.PageTables, v.Shared,
	)
	if m.prev != nil && m.prev.Virtual != nil {
		p := m.prev.Virtual
		fmt.Fprintf(&b, "RAM Delta: avail=%+dB used=%+dB free=%+dB active=%+dB inactive=%+dB buffers=%+dB cached=%+dB\n",
			int64(v.Available)-int64(p.Available),
			int64(v.Used)-int64(p.Used),
			int64(v.Free)-int64(p.Free),
			int64(v.Active)-int64(p.Active),
			int64(v.Inactive)-int64(p.Inactive),
			int64(v.Buffers)-int64(p.Buffers),
			int64(v.Cached)-int64(p.Cached),
		)
	}
	fmt.Fprintf(&b, "Swap: total=%dB used=%dB used%%=%.2f free=%dB sin=%dB sout=%dB pgIn=%d pgOut=%d pgFault=%d pgMajFault=%d",
		s.Total, s.Used, s.UsedPercent, s.Free, s.Sin, s.Sout, s.PgIn, s.PgOut, s.PgFault, s.PgMajFault,
	)
	if m.prev != nil && m.prev.Swap != nil {
		p := m.prev.Swap
		fmt.Fprintf(&b, "\nSwap Delta: used=%+dB free=%+dB sin=%+dB sout=%+dB",
			int64(s.Used)-int64(p.Used),
			int64(s.Free)-int64(p.Free),
			int64(s.Sin)-int64(p.Sin),
			int64(s.Sout)-int64(p.Sout),
		)
	}
	return b.String(), nil
}
