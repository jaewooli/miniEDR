package miniedr

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
)

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

func (d *DISKCapturer) GetInfo() (InfoData, error) {
	if d.curr == nil {
		return InfoData{Summary: "DISKSnapshot(empty)"}, nil
	}

	metrics := make(map[string]float64)

	// Usage 요약: 첫 path 기준으로 보여주기(너무 길어지는 걸 방지)
	usageSummary := "n/a"
	if len(d.Paths) > 0 {
		p := d.Paths[0]
		if u := d.curr.Usage[p]; u != nil && u.Total > 0 {
			usageSummary = fmt.Sprintf("%s used=%.2f%% (%d/%dB)", p, u.UsedPercent, u.Used, u.Total)
			metrics["disk.used_pct"] = u.UsedPercent
			metrics["disk.total_bytes"] = float64(u.Total)
			metrics["disk.used_bytes"] = float64(u.Used)
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
			rRate := float64(rDelta) / sec
			wRate := float64(wDelta) / sec
			ioSummary = fmt.Sprintf("ioRate=read %dB/s write %dB/s",
				uint64(rRate),
				uint64(wRate),
			)
			metrics["disk.read_bytes_per_sec"] = rRate
			metrics["disk.write_bytes_per_sec"] = wRate
		}
	}

	summary := fmt.Sprintf(
		"DISKSnapshot(at=%s, %s, %s, devices=%d)",
		d.curr.At.Format(time.RFC3339),
		usageSummary,
		ioSummary,
		len(d.curr.IO),
	)

	if len(metrics) == 0 {
		metrics = nil
	}
	return InfoData{Summary: summary, Metrics: metrics}, nil
}

// GetVerboseInfo returns per-path usage and per-device IO deltas.
func (d *DISKCapturer) GetVerboseInfo() (string, error) {
	if d.curr == nil {
		return "DISKSnapshot(verbose-empty)", nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "DISKSnapshot(at=%s)\n", d.curr.At.Format(time.RFC3339))

	// Paths usage detail
	if len(d.curr.Usage) > 0 {
		fmt.Fprintf(&b, "Usage:\n")
		paths := make([]string, 0, len(d.curr.Usage))
		for p := range d.curr.Usage {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			u := d.curr.Usage[p]
			if u == nil {
				continue
			}
			inodePart := ""
			if u.InodesTotal > 0 {
				inodePart = fmt.Sprintf(" inodes=%.2f%% (%d/%d)", u.InodesUsedPercent, u.InodesUsed, u.InodesTotal)
			}
			fmt.Fprintf(&b, "- %s fstype=%s used=%.2f%% (%d/%d)%s\n",
				p, u.Fstype, u.UsedPercent, u.Used, u.Total, inodePart)
		}
	}

	// IO counters detail (delta/sec when possible)
	sec := 0.0
	if d.prev != nil {
		sec = d.curr.At.Sub(d.prev.At).Seconds()
	}
	if len(d.curr.IO) > 0 {
		fmt.Fprintf(&b, "IO:\n")
		devs := make([]string, 0, len(d.curr.IO))
		for name := range d.curr.IO {
			devs = append(devs, name)
		}
		sort.Strings(devs)
		for _, name := range devs {
			cur := d.curr.IO[name]
			if sec <= 0 {
				fmt.Fprintf(&b, "- %s readBytes=%d writeBytes=%d readIO=%d writeIO=%d\n",
					name, cur.ReadBytes, cur.WriteBytes, cur.ReadCount, cur.WriteCount)
				continue
			}
			prev, ok := d.prev.IO[name]
			if !ok {
				fmt.Fprintf(&b, "- %s (no-prev) readBytes=%d writeBytes=%d readIO=%d writeIO=%d\n",
					name, cur.ReadBytes, cur.WriteBytes, cur.ReadCount, cur.WriteCount)
				continue
			}
			rb := deltaUint64(prev.ReadBytes, cur.ReadBytes)
			wb := deltaUint64(prev.WriteBytes, cur.WriteBytes)
			ri := deltaUint64(prev.ReadCount, cur.ReadCount)
			wi := deltaUint64(prev.WriteCount, cur.WriteCount)
			fmt.Fprintf(&b, "- %s read=%.0fB/s write=%.0fB/s readIO=%.1f/s writeIO=%.1f/s\n",
				name,
				float64(rb)/sec, float64(wb)/sec,
				float64(ri)/sec, float64(wi)/sec,
			)
		}
	}

	out := strings.TrimSuffix(b.String(), "\n")
	if out == "" {
		return "DISKSnapshot(verbose-empty)", nil
	}
	return out, nil
}

// IsWarm reports whether a previous snapshot exists (needed for IO deltas).
func (d *DISKCapturer) IsWarm() bool {
	return d.prev != nil
}

func deltaUint64(prev, cur uint64) uint64 {
	if cur >= prev {
		return cur - prev
	}
	return 0
}
