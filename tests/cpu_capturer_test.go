package miniedr_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/jaewooli/miniedr"
	"github.com/shirou/gopsutil/v4/cpu"
)

func TestCPUCapturer(t *testing.T) {
	c := &miniedr.CPUCapturer{}

	got, err := c.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "CPUSnapshot(empty)")

	t.Run("error when TimesFn nil", func(t *testing.T) {
		c2 := &miniedr.CPUCapturer{}
		c2.TimesFn = nil
		err := c2.Capture()
		assertError(t, err, "cpu capturer: TimesFn is nil")
	})

	nowCalls := 0
	nowSeq := []time.Time{time.Unix(10, 0), time.Unix(20, 0)}
	c.Now = func() time.Time {
		if nowCalls >= len(nowSeq) {
			return nowSeq[len(nowSeq)-1]
		}
		defer func() { nowCalls++ }()
		return nowSeq[nowCalls]
	}

	perCore := [][]cpu.TimesStat{
		{
			{User: 1, System: 1, Idle: 8},
			{User: 1, System: 1, Idle: 8},
		},
		{
			{User: 3, System: 2, Idle: 9},
			{User: 2, System: 2, Idle: 10},
		},
	}
	total := [][]cpu.TimesStat{
		{
			{User: 2, System: 2, Idle: 16},
		},
		{
			{User: 6, System: 4, Idle: 17},
		},
	}

	call := 0
	c.TimesFn = func(percpu bool) ([]cpu.TimesStat, error) {
		idx := call / 2
		call++
		if idx >= len(perCore) {
			return nil, fmt.Errorf("unexpected times call %d", call)
		}
		if percpu {
			return perCore[idx], nil
		}
		return total[idx], nil
	}

	assertError(t, c.Capture(), "")
	got, err = c.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "CPUSnapshot(at=1970-01-01T09:00:10+09:00, totalUsage=n/a, instant=0.00%)")

	assertError(t, c.Capture(), "")
	got, err = c.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "CPUSnapshot(at=1970-01-01T09:00:20+09:00, totalUsage=85.71%, cpu0=75.0% cpu1=50.0%, instant=0.00%)")
}

func TestCPUCapturerVerbose(t *testing.T) {
	c := &miniedr.CPUCapturer{}

	nowSeq := []time.Time{time.Unix(10, 0), time.Unix(20, 0)}
	nowCalls := 0
	c.Now = func() time.Time {
		if nowCalls >= len(nowSeq) {
			return nowSeq[len(nowSeq)-1]
		}
		defer func() { nowCalls++ }()
		return nowSeq[nowCalls]
	}

	perCore := [][]cpu.TimesStat{
		{
			{User: 1, System: 1, Idle: 8},
			{User: 1, System: 1, Idle: 8},
		},
		{
			{User: 3, System: 2, Idle: 9},
			{User: 2, System: 2, Idle: 10},
		},
	}
	total := [][]cpu.TimesStat{
		{
			{User: 2, System: 2, Idle: 16},
		},
		{
			{User: 6, System: 4, Idle: 17},
		},
	}
	call := 0
	c.TimesFn = func(percpu bool) ([]cpu.TimesStat, error) {
		idx := call / 2
		call++
		if percpu {
			return perCore[idx], nil
		}
		return total[idx], nil
	}

	assertError(t, c.Capture(), "")
	assertError(t, c.Capture(), "")

	got, err := c.GetVerboseInfo()
	assertError(t, err, "")
	want := "" +
		"total usage=85.71% user=6.00s system=4.00s idle=17.00s nice=0.00s iowait=0.00s irq=0.00s softirq=0.00s steal=0.00s guest=0.00s guestNice=0.00s\n" +
		"cpu0 usage=75.00% user=3.00s system=2.00s idle=9.00s nice=0.00s iowait=0.00s irq=0.00s softirq=0.00s steal=0.00s guest=0.00s guestNice=0.00s\n" +
		"cpu1 usage=50.00% user=2.00s system=2.00s idle=10.00s nice=0.00s iowait=0.00s irq=0.00s softirq=0.00s steal=0.00s guest=0.00s guestNice=0.00s"
	assertEqual(t, got, want)
}
