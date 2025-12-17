package miniedr_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/jaewooli/miniedr"
	"github.com/shirou/gopsutil/v4/mem"
)

func TestMemSnapShot(t *testing.T) {
	memCapturer := &miniedr.MEMCapturer{}

	// deterministic time sequence
	nowCalls := 0
	nowSeq := []time.Time{time.Unix(10, 0), time.Unix(20, 0)}
	memCapturer.Now = func() time.Time {
		if nowCalls >= len(nowSeq) {
			return nowSeq[len(nowSeq)-1]
		}
		defer func() { nowCalls++ }()
		return nowSeq[nowCalls]
	}

	vmSeq := []*mem.VirtualMemoryStat{
		{
			Total:     1000,
			Available: 250,
			Free:      100,
			Buffers:   50,
			Cached:    200,
		},
		{
			Total:     2000,
			Available: 1000,
			Free:      500,
			Buffers:   10,
			Cached:    100,
		},
	}
	swapSeq := []*mem.SwapMemoryStat{
		{
			Total: 400,
			Used:  100,
			Free:  300,
			Sin:   1,
			Sout:  2,
		},
		{
			Total: 1000,
			Used:  500,
			Free:  500,
			Sin:   4,
			Sout:  8,
		},
	}
	vmCall := 0
	memCapturer.VirtualFn = func() (*mem.VirtualMemoryStat, error) {
		defer func() { vmCall++ }()
		return vmSeq[vmCall], nil
	}
	swapCall := 0
	memCapturer.SwapFn = func() (*mem.SwapMemoryStat, error) {
		defer func() { swapCall++ }()
		return swapSeq[swapCall], nil
	}

	t.Run("getinfo empty memory snapshot", func(t *testing.T) {
		got, err := memCapturer.GetInfo()
		assertError(t, err, "")
		assertEqual(t, got, "MEMSnapshot(empty)")
	})

	t.Run("capture and getinfo", func(t *testing.T) {
		assertError(t, memCapturer.Capture(), "")
		got, err := memCapturer.GetInfo()
		assertError(t, err, "")
		want := fmt.Sprintf(
			"MEMSnapshot(at=%s, RAM: Total=%dB Avail=%dB UsedApprox=%dB (%.2f%%), Free=%dB Buffers=%dB Cached=%dB; Swap: Total=%dB Used=%dB (%.2f%%) Free=%dB, Sin=%dB Sout=%dB)",
			nowSeq[0].Format(time.RFC3339),
			vmSeq[0].Total, vmSeq[0].Available, vmSeq[0].Total-vmSeq[0].Available, float64(vmSeq[0].Total-vmSeq[0].Available)/float64(vmSeq[0].Total)*100,
			vmSeq[0].Free, vmSeq[0].Buffers, vmSeq[0].Cached,
			swapSeq[0].Total, swapSeq[0].Used, float64(swapSeq[0].Used)/float64(swapSeq[0].Total)*100,
			swapSeq[0].Free, swapSeq[0].Sin, swapSeq[0].Sout,
		)
		assertEqual(t, got, want)

		assertError(t, memCapturer.Capture(), "")
		got, err = memCapturer.GetInfo()
		assertError(t, err, "")
		want = fmt.Sprintf(
			"MEMSnapshot(at=%s, RAM: Total=%dB Avail=%dB UsedApprox=%dB (%.2f%%), Free=%dB Buffers=%dB Cached=%dB; Swap: Total=%dB Used=%dB (%.2f%%) Free=%dB, Sin=%dB Sout=%dB)",
			nowSeq[1].Format(time.RFC3339),
			vmSeq[1].Total, vmSeq[1].Available, vmSeq[1].Total-vmSeq[1].Available, float64(vmSeq[1].Total-vmSeq[1].Available)/float64(vmSeq[1].Total)*100,
			vmSeq[1].Free, vmSeq[1].Buffers, vmSeq[1].Cached,
			swapSeq[1].Total, swapSeq[1].Used, float64(swapSeq[1].Used)/float64(swapSeq[1].Total)*100,
			swapSeq[1].Free, swapSeq[1].Sin, swapSeq[1].Sout,
		)
		assertEqual(t, got, want)
	})

	t.Run("error when VirtualFn nil", func(t *testing.T) {
		m2 := &miniedr.MEMCapturer{
			SwapFn: mem.SwapMemory,
			Now:    time.Now,
		}
		m2.VirtualFn = nil
		err := m2.Capture()
		assertError(t, err, "mem capturer: VirtualFn is nil")
	})
}

func TestMemSnapshotVerbose(t *testing.T) {
	memCapturer := &miniedr.MEMCapturer{}

	nowSeq := []time.Time{time.Unix(10, 0)}
	nowCalls := 0
	memCapturer.Now = func() time.Time {
		if nowCalls >= len(nowSeq) {
			return nowSeq[len(nowSeq)-1]
		}
		defer func() { nowCalls++ }()
		return nowSeq[nowCalls]
	}

	vm := &mem.VirtualMemoryStat{
		Total:        1024,
		Available:    256,
		Used:         768,
		UsedPercent:  75.0,
		Free:         128,
		Active:       10,
		Inactive:     20,
		Wired:        30,
		Buffers:      40,
		Cached:       50,
		SwapCached:   60,
		Dirty:        70,
		WriteBack:    80,
		Slab:         90,
		Sreclaimable: 100,
		Sunreclaim:   110,
		PageTables:   120,
		Shared:       130,
	}
	swap := &mem.SwapMemoryStat{
		Total:       512,
		Used:        128,
		UsedPercent: 25.0,
		Free:        384,
		Sin:         1,
		Sout:        2,
		PgIn:        3,
		PgOut:       4,
		PgFault:     5,
		PgMajFault:  6,
	}
	memCapturer.VirtualFn = func() (*mem.VirtualMemoryStat, error) { return vm, nil }
	memCapturer.SwapFn = func() (*mem.SwapMemoryStat, error) { return swap, nil }

	assertError(t, memCapturer.Capture(), "")

	got, err := memCapturer.GetVerboseInfo()
	assertError(t, err, "")
	want := "MEMSnapshot(at=1970-01-01T09:00:10+09:00)\n" +
		"RAM: total=1024B avail=256B used=768B used%=75.00 free=128B active=10B inactive=20B wired=30B buffers=40B cached=50B swapCached=60B dirty=70B writeback=80B slab=90B sreclaimable=100B sunreclaim=110B pageTables=120B shared=130B\n" +
		"Swap: total=512B used=128B used%=25.00 free=384B sin=1B sout=2B pgIn=3 pgOut=4 pgFault=5 pgMajFault=6"
	assertEqual(t, got, want)
}
