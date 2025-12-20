package capturer_test

import (
	"testing"
	"time"

	"github.com/jaewooli/miniedr/capturer"
	"github.com/shirou/gopsutil/v4/disk"
)

func TestDISKCapturer(t *testing.T) {
	d := &capturer.DISKCapturer{
		Paths: []string{"/mnt"},
	}

	got, err := d.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got.Summary, "DISKSnapshot(empty)")

	t.Run("error when UsageFn nil", func(t *testing.T) {
		d2 := &capturer.DISKCapturer{
			Paths: []string{"/"},
		}
		d2.UsageFn = nil
		err := d2.Capture()
		assertError(t, err, "disk capturer: UsageFn is nil")
	})

	t.Run("error when IOCountersFn nil", func(t *testing.T) {
		d3 := &capturer.DISKCapturer{
			Paths:   []string{"/"},
			UsageFn: func(path string) (*disk.UsageStat, error) { return &disk.UsageStat{Total: 1, Used: 1}, nil },
		}
		d3.IOCountersFn = nil
		err := d3.Capture()
		assertError(t, err, "disk capturer: IOCountersFn is nil")
	})

	nowCalls := 0
	nowSeq := []time.Time{time.Unix(10, 0), time.Unix(20, 0)}
	d.Now = func() time.Time {
		if nowCalls >= len(nowSeq) {
			return nowSeq[len(nowSeq)-1]
		}
		defer func() { nowCalls++ }()
		return nowSeq[nowCalls]
	}

	d.UsageFn = func(path string) (*disk.UsageStat, error) {
		return &disk.UsageStat{
			Path:        path,
			Total:       1000,
			Used:        500,
			UsedPercent: 50.0,
		}, nil
	}

	ioSeq := []map[string]disk.IOCountersStat{
		{
			"sda": {ReadBytes: 100, WriteBytes: 200},
		},
		{
			"sda": {ReadBytes: 300, WriteBytes: 500},
		},
	}
	ioCall := 0
	d.IOCountersFn = func(names ...string) (map[string]disk.IOCountersStat, error) {
		defer func() { ioCall++ }()
		return ioSeq[ioCall], nil
	}

	assertError(t, d.Capture(), "")
	got, err = d.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got.Summary, "DISKSnapshot(at=1970-01-01T09:00:10+09:00, /mnt used=50.00% (500/1000B), ioRate=n/a, devices=1)")

	assertError(t, d.Capture(), "")
	got, err = d.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got.Summary, "DISKSnapshot(at=1970-01-01T09:00:20+09:00, /mnt used=50.00% (500/1000B), ioRate=read 20B/s write 30B/s, devices=1)")
}

func TestDISKCapturerVerbose(t *testing.T) {
	d := &capturer.DISKCapturer{
		Paths: []string{"/mnt"},
	}

	nowSeq := []time.Time{time.Unix(10, 0), time.Unix(20, 0)}
	nowCalls := 0
	d.Now = func() time.Time {
		if nowCalls >= len(nowSeq) {
			return nowSeq[len(nowSeq)-1]
		}
		defer func() { nowCalls++ }()
		return nowSeq[nowCalls]
	}

	d.UsageFn = func(path string) (*disk.UsageStat, error) {
		return &disk.UsageStat{
			Path:        path,
			Total:       1000,
			Used:        500,
			UsedPercent: 50.0,
		}, nil
	}

	ioSeq := []map[string]disk.IOCountersStat{
		{"sda": {ReadBytes: 100, WriteBytes: 200}},
		{"sda": {ReadBytes: 300, WriteBytes: 500}},
	}
	ioCall := 0
	d.IOCountersFn = func(names ...string) (map[string]disk.IOCountersStat, error) {
		defer func() { ioCall++ }()
		return ioSeq[ioCall], nil
	}

	assertError(t, d.Capture(), "")
	assertError(t, d.Capture(), "")

	got, err := d.GetVerboseInfo()
	assertError(t, err, "")
	want := "" +
		"DISKSnapshot(at=1970-01-01T09:00:20+09:00, interval=10s)\n" +
		"Summary: paths=1 (prev=1, delta=+0) devices=1 (prev=1, delta=+0)\n" +
		"Usage:\n" +
		"- /mnt fstype= used=50.00% (500/1000)\n" +
		"IO:\n" +
		"- sda read=20B/s write=30B/s readIO=0.0/s writeIO=0.0/s"
	assertEqual(t, got, want)
}
