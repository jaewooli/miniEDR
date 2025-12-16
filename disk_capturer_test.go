package miniedr_test

import (
	"testing"
	"time"

	"github.com/jaewooli/miniedr"
	"github.com/shirou/gopsutil/v4/disk"
)

func TestDISKCapturer(t *testing.T) {
	d := &miniedr.DISKCapturer{
		Paths: []string{"/mnt"},
	}

	got, err := d.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "DISKSnapshot(empty)")

	t.Run("error when UsageFn nil", func(t *testing.T) {
		d2 := &miniedr.DISKCapturer{
			Paths: []string{"/"},
		}
		d2.UsageFn = nil
		err := d2.Capture()
		assertError(t, err, "disk capturer: UsageFn is nil")
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
	assertEqual(t, got, "DISKSnapshot(at=1970-01-01T09:00:10+09:00, /mnt used=50.00% (500/1000B), ioRate=n/a, devices=1)")

	assertError(t, d.Capture(), "")
	got, err = d.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "DISKSnapshot(at=1970-01-01T09:00:20+09:00, /mnt used=50.00% (500/1000B), ioRate=read 20B/s write 30B/s, devices=1)")
}
