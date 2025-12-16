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
