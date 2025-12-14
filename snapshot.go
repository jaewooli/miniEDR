package miniedr

import (
	"errors"
	"fmt"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"

	"io"
	"math"
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

// -------------------- CPU --------------------

type CPUSnapshot struct {
	At      time.Time
	PerCore []cpu.TimesStat // cpu.Times(percpu=true)
	Total   []cpu.TimesStat // cpu.Times(percpu=false) -> len=1
}

type CPUCapturer struct {
	Now     func() time.Time
	TimesFn func(percpu bool) ([]cpu.TimesStat, error)

	prev *CPUSnapshot
	curr *CPUSnapshot
}

func NewCPUCapturer() *CPUCapturer {
	return &CPUCapturer{
		Now:     time.Now,
		TimesFn: cpu.Times,
	}
}

func (c *CPUCapturer) Capture() error {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.TimesFn == nil {
		return errors.New("cpu capturer: TimesFn is nil")
	}

	per, err := c.TimesFn(true)
	if err != nil {
		return fmt.Errorf("cpu.Times(percpu=true): %w", err)
	}
	tot, err := c.TimesFn(false)
	if err != nil {
		return fmt.Errorf("cpu.Times(percpu=false): %w", err)
	}

	snap := &CPUSnapshot{
		At:      c.Now(),
		PerCore: per,
		Total:   tot,
	}

	c.prev = c.curr
	c.curr = snap
	return nil
}

func (c *CPUCapturer) GetInfo() (string, error) {
	if c.curr == nil {
		return "CPUSnapshot(empty)", nil
	}

	// 델타 기반 usage% 계산(가능하면)
	totalUsage := "n/a"
	if c.prev != nil && len(c.prev.Total) == 1 && len(c.curr.Total) == 1 {
		u, ok := cpuUsagePct(c.prev.Total[0], c.curr.Total[0])
		if ok {
			totalUsage = fmt.Sprintf("%.2f%%", u)
		}
	}

	// 코어별은 너무 길어질 수 있어 top 몇 개만
	coreInfo := ""
	if c.prev != nil && len(c.prev.PerCore) == len(c.curr.PerCore) && len(c.curr.PerCore) > 0 {
		n := min(4, len(c.curr.PerCore))
		var b strings.Builder
		for i := 0; i < n; i++ {
			u, ok := cpuUsagePct(c.prev.PerCore[i], c.curr.PerCore[i])
			if !ok {
				continue
			}
			fmt.Fprintf(&b, "cpu%d=%.1f%% ", i, u)
		}
		coreInfo = strings.TrimSpace(b.String())
		if coreInfo != "" {
			coreInfo = ", " + coreInfo
		}
	}

	return fmt.Sprintf(
		"CPUSnapshot(at=%s, totalUsage=%s%s)",
		c.curr.At.Format(time.RFC3339),
		totalUsage,
		coreInfo,
	), nil
}

// cpuUsagePct: (total - idle) / total * 100 (TimesStat는 초 단위 누적)
func cpuUsagePct(a, b cpu.TimesStat) (float64, bool) {
	ta := totalCPUTime(a)
	tb := totalCPUTime(b)
	if tb < ta {
		return 0, false
	}
	totalDelta := tb - ta
	if totalDelta <= 0 {
		return 0, false
	}

	ia := a.Idle + a.Iowait
	ib := b.Idle + b.Iowait
	if ib < ia {
		return 0, false
	}
	idleDelta := ib - ia

	busy := totalDelta - idleDelta
	pct := (busy / totalDelta) * 100.0
	if math.IsNaN(pct) || math.IsInf(pct, 0) {
		return 0, false
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct, true
}

func totalCPUTime(t cpu.TimesStat) float64 {
	return t.User + t.Nice + t.System + t.Idle + t.Iowait + t.Irq + t.Softirq + t.Steal + t.Guest + t.GuestNice
}

// -------------------- NETWORK --------------------

type NETSnapshot struct {
	At    time.Time
	PerIF map[string]gnet.IOCountersStat // interface name -> counters
}

type NETCapturer struct {
	Now  func() time.Time
	IOFn func(pernic bool) ([]gnet.IOCountersStat, error) // net.IOCounters

	prev *NETSnapshot
	curr *NETSnapshot
}

func NewNETCapturer() *NETCapturer {
	return &NETCapturer{
		Now:  time.Now,
		IOFn: gnet.IOCounters,
	}
}

func (n *NETCapturer) Capture() error {
	if n.Now == nil {
		n.Now = time.Now
	}
	if n.IOFn == nil {
		return errors.New("net capturer: IOFn is nil")
	}

	list, err := n.IOFn(true)
	if err != nil {
		return fmt.Errorf("net.IOCounters(pernic=true): %w", err)
	}

	m := make(map[string]gnet.IOCountersStat, len(list))
	for _, x := range list {
		m[x.Name] = x
	}

	snap := &NETSnapshot{At: n.Now(), PerIF: m}
	n.prev = n.curr
	n.curr = snap
	return nil
}

func (n *NETCapturer) GetInfo() (string, error) {
	if n.curr == nil {
		return "NETSnapshot(empty)", nil
	}

	// 델타 기반 초당 트래픽(rate) 계산: (BytesDelta / seconds)
	rxRate, txRate := "n/a", "n/a"
	if n.prev != nil {
		sec := n.curr.At.Sub(n.prev.At).Seconds()
		if sec > 0 {
			var rxDelta, txDelta uint64
			for ifname, cur := range n.curr.PerIF {
				prev, ok := n.prev.PerIF[ifname]
				if !ok {
					continue
				}
				if cur.BytesRecv >= prev.BytesRecv {
					rxDelta += cur.BytesRecv - prev.BytesRecv
				}
				if cur.BytesSent >= prev.BytesSent {
					txDelta += cur.BytesSent - prev.BytesSent
				}
			}
			rxRate = fmt.Sprintf("%dB/s", uint64(float64(rxDelta)/sec))
			txRate = fmt.Sprintf("%dB/s", uint64(float64(txDelta)/sec))
		}
	}

	return fmt.Sprintf(
		"NETSnapshot(at=%s, ifaces=%d, rxRate=%s, txRate=%s)",
		n.curr.At.Format(time.RFC3339),
		len(n.curr.PerIF),
		rxRate, txRate,
	), nil
}

// -------------------- DISK --------------------

// Disk는 "상태(Usage)"와 "I/O 카운터" 둘 다 보는 게 EDR에 유용함.
// Usage는 mountpoint 별로 잡는 게 일반적이라, Paths로 설정 가능하게.
type DISKSnapshot struct {
	At    time.Time
	Usage map[string]*disk.UsageStat     // path/mount -> usage
	IO    map[string]disk.IOCountersStat // device -> io counters
}

type DISKCapturer struct {
	Now func() time.Time

	Paths []string // 예: []string{"/"} (리눅스), 윈도우는 []string{"C:\\"}

	UsageFn      func(path string) (*disk.UsageStat, error)                    // disk.Usage
	IOCountersFn func(names ...string) (map[string]disk.IOCountersStat, error) // disk.IOCounters

	prev *DISKSnapshot
	curr *DISKSnapshot
}

func NewDISKCapturer(paths ...string) *DISKCapturer {
	if len(paths) == 0 {
		paths = []string{"/"}
	}
	return &DISKCapturer{
		Now:          time.Now,
		Paths:        paths,
		UsageFn:      disk.Usage,
		IOCountersFn: disk.IOCounters,
	}
}

func (d *DISKCapturer) Capture() error {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.UsageFn == nil {
		return errors.New("disk capturer: UsageFn is nil")
	}
	if d.IOCountersFn == nil {
		return errors.New("disk capturer: IOCountersFn is nil")
	}

	usage := make(map[string]*disk.UsageStat, len(d.Paths))
	for _, p := range d.Paths {
		u, err := d.UsageFn(p)
		if err != nil {
			return fmt.Errorf("disk.Usage(%q): %w", p, err)
		}
		usage[p] = u
	}

	ioMap, err := d.IOCountersFn() // all devices
	if err != nil {
		return fmt.Errorf("disk.IOCounters(): %w", err)
	}

	snap := &DISKSnapshot{
		At:    d.Now(),
		Usage: usage,
		IO:    ioMap,
	}

	d.prev = d.curr
	d.curr = snap
	return nil
}

func (d *DISKCapturer) GetInfo() (string, error) {
	if d.curr == nil {
		return "DISKSnapshot(empty)", nil
	}

	// Usage 요약: 첫 path 기준으로 보여주기(너무 길어지는 걸 방지)
	usageSummary := "n/a"
	if len(d.Paths) > 0 {
		p := d.Paths[0]
		if u := d.curr.Usage[p]; u != nil && u.Total > 0 {
			usageSummary = fmt.Sprintf("%s used=%.2f%% (%d/%dB)", p, u.UsedPercent, u.Used, u.Total)
		}
	}

	// IO rate: read/write bytes per sec (all devices 합)
	ioSummary := "ioRate=n/a"
	if d.prev != nil {
		sec := d.curr.At.Sub(d.prev.At).Seconds()
		if sec > 0 {
			var rDelta, wDelta uint64
			for dev, cur := range d.curr.IO {
				prev, ok := d.prev.IO[dev]
				if !ok {
					continue
				}
				if cur.ReadBytes >= prev.ReadBytes {
					rDelta += cur.ReadBytes - prev.ReadBytes
				}
				if cur.WriteBytes >= prev.WriteBytes {
					wDelta += cur.WriteBytes - prev.WriteBytes
				}
			}
			ioSummary = fmt.Sprintf("ioRate=read %dB/s write %dB/s",
				uint64(float64(rDelta)/sec),
				uint64(float64(wDelta)/sec),
			)
		}
	}

	return fmt.Sprintf(
		"DISKSnapshot(at=%s, %s, %s, devices=%d)",
		d.curr.At.Format(time.RFC3339),
		usageSummary,
		ioSummary,
		len(d.curr.IO),
	), nil
}

// -------------------- util --------------------

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
