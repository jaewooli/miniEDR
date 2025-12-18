package capturer_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jaewooli/miniedr/capturer"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

func TestCapturerTableDriven(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "cpu avg percent included",
			run: func(t *testing.T) {
				call := 0
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

				c := &capturer.CPUCapturer{
					Now: time.Now,
					TimesFn: func(percpu bool) ([]cpu.TimesStat, error) {
						idx := call / 2
						call++
						if idx >= len(perCore) {
							return nil, fmt.Errorf("unexpected times call %d", call)
						}
						if percpu {
							return perCore[idx], nil
						}
						return total[idx], nil
					},
				}
				_ = c.Capture()
				_ = c.Capture()
				info, _ := c.GetInfo()
				assertContains(t, info.Summary, "totalUsage=85.71%")
			},
		},
		{
			name: "mem includes swap percent",
			run: func(t *testing.T) {
				m := &capturer.MEMCapturer{
					Now: func() time.Time { return time.Unix(0, 0) },
					VirtualFn: func() (*mem.VirtualMemoryStat, error) {
						return &mem.VirtualMemoryStat{Total: 100, Available: 50, Free: 10, Buffers: 5, Cached: 5}, nil
					},
					SwapFn: func() (*mem.SwapMemoryStat, error) {
						return &mem.SwapMemoryStat{Total: 80, Used: 20, Free: 60, UsedPercent: 25}, nil
					},
				}
				_ = m.Capture()
				info, _ := m.GetInfo()
				assertContains(t, info.Summary, "(25.00%)")
			},
		},
		{
			name: "disk io rate computed",
			run: func(t *testing.T) {
				d := &capturer.DISKCapturer{
					Paths: []string{"/"},
					Now: func() time.Time {
						return time.Unix(0, 0)
					},
					UsageFn: func(path string) (*disk.UsageStat, error) {
						return &disk.UsageStat{Total: 1000, Used: 500, UsedPercent: 50}, nil
					},
					IOCountersFn: func(names ...string) (map[string]disk.IOCountersStat, error) {
						return map[string]disk.IOCountersStat{"sda": {ReadBytes: 100, WriteBytes: 200}}, nil
					},
				}
				_ = d.Capture()
				d.Now = func() time.Time { return time.Unix(5, 0) }
				d.IOCountersFn = func(names ...string) (map[string]disk.IOCountersStat, error) {
					return map[string]disk.IOCountersStat{"sda": {ReadBytes: 600, WriteBytes: 700}}, nil
				}
				_ = d.Capture()
				info, _ := d.GetInfo()
				assertContains(t, info.Summary, "read 100B/s")
				assertContains(t, info.Summary, "write 100B/s")
			},
		},
		{
			name: "net rates sum multiple ifaces",
			run: func(t *testing.T) {
				n := &capturer.NETCapturer{
					Now: func() time.Time { return time.Unix(0, 0) },
					IOFn: func(pernic bool) ([]gnet.IOCountersStat, error) {
						return []gnet.IOCountersStat{
							{Name: "eth0", BytesRecv: 100, BytesSent: 50},
							{Name: "eth1", BytesRecv: 200, BytesSent: 150},
						}, nil
					},
				}
				_ = n.Capture()
				n.Now = func() time.Time { return time.Unix(5, 0) }
				n.IOFn = func(pernic bool) ([]gnet.IOCountersStat, error) {
					return []gnet.IOCountersStat{
						{Name: "eth0", BytesRecv: 1100, BytesSent: 550},
						{Name: "eth1", BytesRecv: 2200, BytesSent: 1150},
					}, nil
				}
				_ = n.Capture()
				info, _ := n.GetInfo()
				assertContains(t, info.Summary, "rxRate=600B/s")
				assertContains(t, info.Summary, "txRate=300B/s")
			},
		},
		{
			name: "filewatch detects created",
			run: func(t *testing.T) {
				dir := t.TempDir()
				w := &capturer.FileChangeCapturer{
					Paths:    []string{dir},
					MaxFiles: 10,
					WalkFn:   filepath.WalkDir,
					Now:      func() time.Time { return time.Unix(0, 0) },
				}
				_ = w.Capture()
				w.Now = func() time.Time { return time.Unix(10, 0) }
				createFile(t, dir, "a.txt")
				_ = w.Capture()
				info, _ := w.GetInfo()
				assertContains(t, info.Summary, "events=1")
			},
		},
		{
			name: "proc detects new pid",
			run: func(t *testing.T) {
				p := &capturer.ProcCapturer{
					Now: func() time.Time { return time.Unix(0, 0) },
					ProcessesFn: func() ([]*process.Process, error) {
						return []*process.Process{{Pid: 1}}, nil
					},
					NameFn:       func(proc *process.Process) (string, error) { return "p1", nil },
					ExeFn:        func(proc *process.Process) (string, error) { return "/bin/p1", nil },
					CmdlineFn:    func(proc *process.Process) (string, error) { return "p1", nil },
					PPIDFn:       func(proc *process.Process) (int32, error) { return 0, nil },
					CreateTimeFn: func(proc *process.Process) (int64, error) { return 0, nil },
				}
				_ = p.Capture()
				p.Now = func() time.Time { return time.Unix(10, 0) }
				p.ProcessesFn = func() ([]*process.Process, error) {
					return []*process.Process{{Pid: 1}, {Pid: 2}}, nil
				}
				_ = p.Capture()
				info, _ := p.GetInfo()
				assertContains(t, info.Summary, "new=1")
			},
		},
		{
			name: "persist detects added",
			run: func(t *testing.T) {
				src := &stubPersistSource{
					name: "s",
					snapshots: []map[string]string{
						{"a": "1"},
						{"a": "1", "b": "2"},
					},
				}
				p := &capturer.PersistCapturer{
					Now:     func() time.Time { return time.Unix(0, 0) },
					Sources: []capturer.PersistSource{src},
				}
				_ = p.Capture()
				p.Now = func() time.Time { return time.Unix(5, 0) }
				_ = p.Capture()
				info, _ := p.GetInfo()
				assertContains(t, info.Summary, "added=1")
			},
		},
		{
			name: "disk error bubble",
			run: func(t *testing.T) {
				d := &capturer.DISKCapturer{
					Paths:   []string{"/"},
					UsageFn: func(path string) (*disk.UsageStat, error) { return nil, fmt.Errorf("boom") },
					IOCountersFn: func(names ...string) (map[string]disk.IOCountersStat, error) {
						return map[string]disk.IOCountersStat{}, nil
					},
				}
				err := d.Capture()
				assertContains(t, err.Error(), "boom")
			},
		},
		{
			name: "net handles new iface",
			run: func(t *testing.T) {
				n := &capturer.NETCapturer{
					Now: func() time.Time { return time.Unix(0, 0) },
					IOFn: func(pernic bool) ([]gnet.IOCountersStat, error) {
						return []gnet.IOCountersStat{{Name: "eth0", BytesRecv: 10, BytesSent: 10}}, nil
					},
				}
				_ = n.Capture()
				n.Now = func() time.Time { return time.Unix(5, 0) }
				n.IOFn = func(pernic bool) ([]gnet.IOCountersStat, error) {
					return []gnet.IOCountersStat{{Name: "eth1", BytesRecv: 100, BytesSent: 200}}, nil
				}
				_ = n.Capture()
				info, _ := n.GetInfo()
				assertContains(t, info.Summary, "rxRate=0B/s")
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, tt.run)
	}
}

func createFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}
}
