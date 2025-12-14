package miniedr

import (
	"errors"
	"fmt"
	"github.com/shirou/gopsutil/v4/mem"
	"io"
	"strings"
	"time"
)

const (
	memSnapshotText = "MEMSnapshot(at=%s, RAM: Total=%dB Avail=%dB UsedApprox=%dB (%.2f%%), Free=%dB Buffers=%dB Cached=%dB; Swap: Total=%dB Used=%dB (%.2f%%) Free=%dB, Sin=%dB Sout=%dB)"
)

type Info interface {
	GetInfo() (string, error)
}

type Capturer interface {
	Info
	Capture() error
}

type MEMSnapshot struct {
	At time.Time

	Virtual *mem.VirtualMemoryStat // Total, Available, Free, Buffers, Cached, Used, etc.
	Swap    *mem.SwapMemoryStat    // Total, Used, Free, Sin, Sout, etc.
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
		return fmt.Errorf("mem.VirtualMemory: %w", err)
	}
	s, err := m.SwapFn()
	if err != nil {
		return fmt.Errorf("mem.SwapMemory: %w", err)
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

	usedApprox := uint64(0)
	usedPctApprox := 0.0
	if v.Total > 0 {
		if v.Total >= v.Available {
			usedApprox = v.Total - v.Available
		}
		usedPctApprox = float64(usedApprox) / float64(v.Total) * 100.0
	}

	swapPct := 0.0
	if s.Total > 0 {
		swapPct = float64(s.Used) / float64(s.Total) * 100.0
	}

	return fmt.Sprintf(
		memSnapshotText,
		m.snapshot.At.Format(time.RFC3339),
		v.Total, v.Available, usedApprox, usedPctApprox, v.Free, v.Buffers, v.Cached,
		s.Total, s.Used, swapPct, s.Free, s.Sin, s.Sout,
	), nil
}

// ---------- SnapshotManager -----------
type SnapshotManager struct {
	out       io.Writer
	capturers []Capturer
}

func (sm *SnapshotManager) Capture() error {

	if len(sm.capturers) == 0 {
		return errors.New("no capturer is in snapshot manager")
	}
	for i, snapshot := range sm.capturers {
		if err := snapshot.Capture(); err != nil {
			return fmt.Errorf("snapshot manager: capturer[%d](%T) capture failed: %q", i, snapshot, err.Error())
		}

	}
	return nil
}

func (sm *SnapshotManager) GetInfo() (string, error) {
	if len(sm.capturers) == 0 {
		return "SnapshotManager(capturers=0)", nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "SnapshotManager(out=%T, capturers=%d)\n", sm.out, len(sm.capturers))

	for i, c := range sm.capturers {
		info, err := c.GetInfo()
		if err != nil {
			return "", fmt.Errorf("snapshot manager: capturer[%d](%T) GetInfo failed: %w", i, c, err)
		}
		fmt.Fprintf(&b, "- [%d] %T: %s\n", i, c, info)
	}

	return b.String(), nil
}

func NewSnapshotManager(out io.Writer, capturers []Capturer) *SnapshotManager {
	return &SnapshotManager{
		out:       out,
		capturers: capturers,
	}
}
