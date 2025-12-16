package miniedr

import (
	"errors"
	"fmt"
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

	m.snapshot = MEMSnapshot{
		At:      now(),
		Virtual: v,
		Swap:    s,
	}
	return nil
}

func (m *MEMCapturer) GetInfo() (string, error) {
	if m.snapshot.Virtual == nil || m.snapshot.Swap == nil {
		return "MEMSnapshot(empty)", nil
	}

	v := m.snapshot.Virtual
	s := m.snapshot.Swap

	used := v.Total - v.Available
	usedPct := float64(used) / float64(v.Total) * 100
	swapPct := float64(s.Used) / float64(s.Total) * 100

	return fmt.Sprintf(
		memSnapshotText,
		m.snapshot.At.Format(time.RFC3339),
		v.Total, v.Available, used, usedPct,
		v.Free, v.Buffers, v.Cached,
		s.Total, s.Used, swapPct,
		s.Free, s.Sin, s.Sout,
	), nil
}
