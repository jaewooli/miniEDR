package miniedr

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
)

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
