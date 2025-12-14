package miniedr

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/shirou/gopsutil/v4/mem"
	"io"
	"reflect"
	"time"
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
	Now func() time.Time

	VirtualFn func() (*mem.VirtualMemoryStat, error)
	SwapFn    func() (*mem.SwapMemoryStat, error)

	Snapshot MEMSnapshot
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

	v, err := m.VirtualFn()
	if err != nil {
		return fmt.Errorf("mem.VirtualMemory: %w", err)
	}
	s, err := m.SwapFn()
	if err != nil {
		return fmt.Errorf("mem.SwapMemory: %w", err)
	}

	m.Snapshot = MEMSnapshot{
		At:      now(),
		Virtual: v,
		Swap:    s,
	}
	return nil
}

func (m *MEMCapturer) GetInfo() (string, error) {
	if m.Snapshot.Virtual == nil || m.Snapshot.Swap == nil {
		return "MEMSnapshot(empty)", nil
	}

	v := m.Snapshot.Virtual
	s := m.Snapshot.Swap

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
		"MEMSnapshot(at=%s, RAM: Total=%dB Avail=%dB UsedApprox=%dB (%.2f%%), Free=%dB Buffers=%dB Cached=%dB; Swap: Total=%dB Used=%dB (%.2f%%) Free=%dB, Sin=%dB Sout=%dB)",
		m.Snapshot.At.Format(time.RFC3339),
		v.Total, v.Available, usedApprox, usedPctApprox, v.Free, v.Buffers, v.Cached,
		s.Total, s.Used, swapPct, s.Free, s.Sin, s.Sout,
	), nil
}

type SnapshotManager struct {
	out       io.Writer
	capturers []Capturer
}

func (s *SnapshotManager) Capture() error {

	if len(s.capturers) == 0 {
		return errors.New("no snapshot is in snapshot manager")
	}
	for _, snapshot := range s.capturers {
		err := snapshot.Capture()
		if err != nil {
			return fmt.Errorf("error in snapshot capturing: %q", err.Error())
		}

	}
	return nil
}

func (s *SnapshotManager) GetInfo() (string, error) {
	info := &bytes.Buffer{}

	fmt.Fprintf(info, "out: %v\ncapturers: [", reflect.TypeOf(s.out))
	infoString := info.String()

	capturersInfo := reflect.ValueOf(s.capturers)

	fmt.Fprintf(info, "%v %d", capturersInfo.Kind(), capturersInfo.Len())
	for i := range capturersInfo.Len() {
		ptrCapturer := capturersInfo.Index(i)
		infoString += fmt.Sprintf("%v", ptrCapturer.Elem())
	}
	infoString += "]"

	return infoString, nil
}

func NewSnapshotManager(out io.Writer, capturers []Capturer) *SnapshotManager {
	return &SnapshotManager{
		out:       out,
		capturers: capturers,
	}
}
