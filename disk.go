package miniedr

import (
	"errors"
	"fmt"
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
