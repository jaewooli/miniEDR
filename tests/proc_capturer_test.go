package miniedr_test

import (
	"testing"
	"time"

	"github.com/jaewooli/miniedr"
	"github.com/shirou/gopsutil/v4/process"
)

func TestProcCapturerVerbose(t *testing.T) {
	nowSeq := []time.Time{time.Unix(10, 0), time.Unix(20, 0)}
	nowCalls := 0
	c := &miniedr.ProcCapturer{
		Now: func() time.Time {
			if nowCalls >= len(nowSeq) {
				return nowSeq[len(nowSeq)-1]
			}
			defer func() { nowCalls++ }()
			return nowSeq[nowCalls]
		},
	}

	// stub process list per capture
	procsSeq := [][]*process.Process{
		{
			{Pid: 1},
			{Pid: 2},
		},
		{
			{Pid: 2},
			{Pid: 3},
		},
	}
	procCall := 0
	c.ProcessesFn = func() ([]*process.Process, error) {
		defer func() { procCall++ }()
		return procsSeq[procCall], nil
	}

	name := map[int32]string{1: "proc1", 2: "proc2", 3: "proc3"}
	exe := map[int32]string{1: "/bin/proc1", 2: "/bin/proc2", 3: "/bin/proc3"}
	cmd := map[int32]string{1: "cmd1", 2: "cmd2", 3: "proc3 --flag"}
	ppid := map[int32]int32{1: 0, 2: 1, 3: 1}
	ctMs := map[int32]int64{1: 0, 2: 1000, 3: 20_000}

	c.NameFn = func(p *process.Process) (string, error) { return name[p.Pid], nil }
	c.ExeFn = func(p *process.Process) (string, error) { return exe[p.Pid], nil }
	c.CmdlineFn = func(p *process.Process) (string, error) { return cmd[p.Pid], nil }
	c.PPIDFn = func(p *process.Process) (int32, error) { return ppid[p.Pid], nil }
	c.CreateTimeFn = func(p *process.Process) (int64, error) { return ctMs[p.Pid], nil }

	assertError(t, c.Capture(), "")
	assertError(t, c.Capture(), "")

	got, err := c.GetVerboseInfo()
	assertError(t, err, "")
	want := "" +
		"ProcSnapshot(at=1970-01-01T09:00:20+09:00, total=2, new=1, dead=1)\n" +
		"New:\n" +
		"- pid=3 ppid=1 name=proc3 exe=/bin/proc3 cmd=proc3 --flag started=1970-01-01T09:00:20+09:00\n" +
		"Dead:\n" +
		"- pid=1 name=proc1 exe=/bin/proc1 cmd=cmd1"
	assertEqual(t, got, want)
}
