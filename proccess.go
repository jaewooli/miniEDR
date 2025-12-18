package miniedr

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

type ProcMeta struct {
	PID        int32
	PPID       int32
	Name       string
	Exe        string
	Cmdline    string
	CreateTime time.Time
}

type ProcSnapshot struct {
	At    time.Time
	Procs map[int32]ProcMeta // pid -> meta

	// Diff (computed at Capture time when prev exists)
	NewPIDs  []int32
	DeadPIDs []int32
}

type ProcCapturer struct {
	Now func() time.Time

	// ProcessesFn returns current process list.
	ProcessesFn func() ([]*process.Process, error)

	// Optional per-process getters. If nil, sensible defaults are used.
	NameFn       func(p *process.Process) (string, error)
	ExeFn        func(p *process.Process) (string, error)
	CmdlineFn    func(p *process.Process) (string, error)
	PPIDFn       func(p *process.Process) (int32, error)
	CreateTimeFn func(p *process.Process) (int64, error) // milliseconds since epoch

	prev *ProcSnapshot
	curr *ProcSnapshot
}

func NewProcCapturer() *ProcCapturer {
	return &ProcCapturer{
		Now:         time.Now,
		ProcessesFn: process.Processes,
	}
}

func (c *ProcCapturer) Capture() error {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.ProcessesFn == nil {
		return errors.New("proc capturer: ProcessesFn is nil")
	}

	procs, err := c.ProcessesFn()
	if err != nil {
		return fmt.Errorf("process.Processes: %w", err)
	}

	// wire defaults lazily
	if c.NameFn == nil {
		c.NameFn = func(p *process.Process) (string, error) { return p.Name() }
	}
	if c.ExeFn == nil {
		c.ExeFn = func(p *process.Process) (string, error) { return p.Exe() }
	}
	if c.CmdlineFn == nil {
		c.CmdlineFn = func(p *process.Process) (string, error) { return p.Cmdline() }
	}
	if c.PPIDFn == nil {
		c.PPIDFn = func(p *process.Process) (int32, error) { return p.Ppid() }
	}
	if c.CreateTimeFn == nil {
		c.CreateTimeFn = func(p *process.Process) (int64, error) { return p.CreateTime() }
	}

	snap := &ProcSnapshot{
		At:    c.Now(),
		Procs: make(map[int32]ProcMeta, len(procs)),
	}

	for _, p := range procs {
		if p == nil {
			continue
		}
		pid := p.Pid

		// Best-effort metadata collection. On errors, keep empty fields.
		name, _ := c.NameFn(p)
		exe, _ := c.ExeFn(p)
		cmd, _ := c.CmdlineFn(p)
		ppid, _ := c.PPIDFn(p)
		ctMs, _ := c.CreateTimeFn(p)

		ct := time.Time{}
		if ctMs > 0 {
			ct = time.UnixMilli(ctMs)
		}

		snap.Procs[pid] = ProcMeta{
			PID:        pid,
			PPID:       ppid,
			Name:       name,
			Exe:        exe,
			Cmdline:    cmd,
			CreateTime: ct,
		}
	}

	// Diff
	if c.curr != nil {
		for pid := range snap.Procs {
			if _, ok := c.curr.Procs[pid]; !ok {
				snap.NewPIDs = append(snap.NewPIDs, pid)
			}
		}
		for pid := range c.curr.Procs {
			if _, ok := snap.Procs[pid]; !ok {
				snap.DeadPIDs = append(snap.DeadPIDs, pid)
			}
		}
		sort.Slice(snap.NewPIDs, func(i, j int) bool { return snap.NewPIDs[i] < snap.NewPIDs[j] })
		sort.Slice(snap.DeadPIDs, func(i, j int) bool { return snap.DeadPIDs[i] < snap.DeadPIDs[j] })
	}

	c.prev = c.curr
	c.curr = snap
	return nil
}

func (c *ProcCapturer) GetInfo() (InfoData, error) {
	if c.curr == nil {
		return InfoData{Summary: "ProcSnapshot(empty)"}, nil
	}
	// show up to 3 new procs in short form
	var examples []string
	for i, pid := range c.curr.NewPIDs {
		if i >= 3 {
			break
		}
		m := c.curr.Procs[pid]
		ex := m.Name
		if ex == "" {
			ex = fmt.Sprintf("pid=%d", pid)
		} else {
			ex = fmt.Sprintf("%s(pid=%d)", ex, pid)
		}
		examples = append(examples, ex)
	}
	exStr := ""
	if len(examples) > 0 {
		exStr = ", new=[" + strings.Join(examples, ", ") + "]"
		if len(c.curr.NewPIDs) > len(examples) {
			exStr += fmt.Sprintf("(+%d)", len(c.curr.NewPIDs)-len(examples))
		}
	}

	summary := fmt.Sprintf(
		"ProcSnapshot(at=%s, procs=%d, new=%d, dead=%d%s)",
		c.curr.At.Format(time.RFC3339),
		len(c.curr.Procs),
		len(c.curr.NewPIDs),
		len(c.curr.DeadPIDs),
		exStr,
	)
	metrics := map[string]float64{
		"proc.total": float64(len(c.curr.Procs)),
		"proc.new":   float64(len(c.curr.NewPIDs)),
		"proc.dead":  float64(len(c.curr.DeadPIDs)),
	}
	return InfoData{Summary: summary, Metrics: metrics}, nil
}

// GetVerboseInfo returns detailed lists of new/dead processes.
func (c *ProcCapturer) GetVerboseInfo() (string, error) {
	if c.curr == nil {
		return "ProcSnapshot(verbose-empty)", nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "ProcSnapshot(at=%s, total=%d, new=%d, dead=%d)\n",
		c.curr.At.Format(time.RFC3339),
		len(c.curr.Procs),
		len(c.curr.NewPIDs),
		len(c.curr.DeadPIDs),
	)

	if len(c.curr.NewPIDs) > 0 {
		fmt.Fprintf(&b, "New:\n")
		news := append([]int32{}, c.curr.NewPIDs...)
		sort.Slice(news, func(i, j int) bool { return news[i] < news[j] })
		limit := min(15, len(news))
		for i := 0; i < limit; i++ {
			pid := news[i]
			m := c.curr.Procs[pid]
			fmt.Fprintf(&b, "- pid=%d ppid=%d name=%s exe=%s cmd=%s started=%s\n",
				m.PID, m.PPID, m.Name, m.Exe, shorten(m.Cmdline, 120), m.CreateTime.Format(time.RFC3339))
		}
		if extra := len(news) - limit; extra > 0 {
			fmt.Fprintf(&b, "  ... (+%d more)\n", extra)
		}
	}

	if len(c.curr.DeadPIDs) > 0 {
		fmt.Fprintf(&b, "Dead:\n")
		dead := append([]int32{}, c.curr.DeadPIDs...)
		sort.Slice(dead, func(i, j int) bool { return dead[i] < dead[j] })
		limit := min(15, len(dead))
		for i := 0; i < limit; i++ {
			pid := dead[i]
			var meta ProcMeta
			if c.prev != nil {
				meta = c.prev.Procs[pid]
			}
			fmt.Fprintf(&b, "- pid=%d name=%s exe=%s cmd=%s\n",
				pid, meta.Name, meta.Exe, shorten(meta.Cmdline, 120))
		}
		if extra := len(dead) - limit; extra > 0 {
			fmt.Fprintf(&b, "  ... (+%d more)\n", extra)
		}
	}

	return strings.TrimSuffix(b.String(), "\n"), nil
}

// IsWarm reports whether a previous snapshot exists (needed for new/dead deltas).
func (c *ProcCapturer) IsWarm() bool {
	return c.prev != nil
}

func shorten(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
