package miniedr_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jaewooli/miniedr"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
)

type StubCapturer struct {
}

func (s *StubCapturer) GetInfo() (string, error) {
	return "", nil
}
func (s *StubCapturer) Capture() error {
	return nil
}

type stubPersistSource struct {
	name      string
	snapshots []map[string]string
	call      int
}

func (s *stubPersistSource) Name() string { return s.name }
func (s *stubPersistSource) Snapshot() (map[string]string, error) {
	if s.call >= len(s.snapshots) {
		return map[string]string{}, nil
	}
	src := s.snapshots[s.call]
	s.call++
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out, nil
}

const (
	exampleconfig = `capturers:
  cpu:
    enabled: true

  conn:
    enabled: true
    kind: all

  disk:
    enabled: true
    paths: ["/"]       

  filewatch:
    enabled: true
    paths: ["/tmp", "/var/tmp"]
    max_files: 50000

  mem:
    enabled: true

  net:
    enabled: true

  persist:
    enabled: true 

  proc:
    enabled: true
`
)

func TestSnapshot(t *testing.T) {
	out := &bytes.Buffer{}
	capturers := []miniedr.Capturer{}

	t.Run("create new empty SnapshotManager", func(t *testing.T) {
		snapshotManager := miniedr.NewSnapshotManager(out, capturers)

		got, err := snapshotManager.GetInfo()

		assertError(t, err, "")
		assertEqual(t, got, "SnapshotManager(capturers=0)\n")

		t.Run("capture for SnapshotManager", func(t *testing.T) {
			err2 := snapshotManager.Capture()
			assertError(t, err2, "no capturer is in snapshot manager")
		})

	})

	t.Run("create new not empty SnapshotManager", func(t *testing.T) {
		stubCapturer := StubCapturer{}
		stubAppendedCapturers := append(capturers, &stubCapturer)

		snapshotManager := miniedr.NewSnapshotManager(out, stubAppendedCapturers)
		got, err := snapshotManager.GetInfo()

		assertError(t, err, "")
		assertEqual(t, got, "SnapshotManager(out=*bytes.Buffer, capturers=1)\n- [0] *miniedr_test.StubCapturer: \n")

		t.Run("capture for SnapshotManager", func(t *testing.T) {
			err2 := snapshotManager.Capture()
			assertError(t, err2, "")
		})
	})
}

func TestCapturersBuilder(t *testing.T) {
	gotBuilder := miniedr.NewCapturersBuilder()
	got, err := gotBuilder.Build()

	assertError(t, err, "")

	want := []miniedr.Capturer{
		miniedr.NewCPUCapturer(),
		miniedr.NewConnCapturer("all"),
		miniedr.NewDISKCapturer(),
		miniedr.NewFileWatchCapturer(),
		miniedr.NewMEMCapturer(),
		miniedr.NewNETCapturer(),
		miniedr.NewPersistCapturer(),
		miniedr.NewProcCapturer(),
	}

	assertCapturers(t, got, want)

	t.Run("setting config", func(t *testing.T) {
		gotBuilder := miniedr.NewCapturersBuilder()
		gotBuilder.SetConfig(exampleconfig)

		got, err := gotBuilder.Build()

		assertError(t, err, "")

		want = miniedr.Capturers{
			miniedr.NewCPUCapturer(),
			miniedr.NewConnCapturer("all"),
			miniedr.NewDISKCapturer(),
			miniedr.NewFileWatchCapturer(),
			miniedr.NewMEMCapturer(),
			miniedr.NewNETCapturer(),
			miniedr.NewPersistCapturer(),
			miniedr.NewProcCapturer(),
		}
		assertCapturers(t, got, want)
	})
}

func assertCapturers(t *testing.T, got, want miniedr.Capturers) {
	t.Helper()
	for i, capturer := range got {
		capturerVal := reflect.ValueOf(capturer).Elem()
		wantVal := reflect.ValueOf(want[i]).Elem()

		if capturerVal.Type() != wantVal.Type() {
			t.Errorf("want %+v, got %+v", capturer, want[i])
		}

		for i := 0; i < capturerVal.NumField(); i++ {
			gotField := capturerVal.Field(i)
			wantField := capturerVal.Field(i)

			if gotField.Kind() == reflect.Func {
				assertEqual(t, fmt.Sprintf("%v", gotField), fmt.Sprintf("%v", wantField))
			} else if gotField.Kind() == reflect.Pointer {
				if !(gotField.IsNil() && wantField.IsNil()) {
					t.Errorf("want %+v, got  %+v", wantField, gotField)
				}
			} else {
				if gotField.CanInterface() {

					assertEqual(t, capturerVal.Field(i).Interface(), wantVal.Field(i).Interface())
				} else {
					// assertCapturers(t, capturerVal.Field(i).Interface(), wantVal.Field(i).Interface())
				}
			}
		}
	}
}

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
}

func TestCPUCapturer(t *testing.T) {
	c := &miniedr.CPUCapturer{}

	got, err := c.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "CPUSnapshot(empty)")

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
			{User: 5, System: 4, Idle: 19},
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
	assertEqual(t, got, "CPUSnapshot(at=1970-01-01T09:00:10+09:00, totalUsage=n/a)")

	assertError(t, c.Capture(), "")
	got, err = c.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "CPUSnapshot(at=1970-01-01T09:00:20+09:00, totalUsage=62.50%, cpu0=75.0% cpu1=50.0%)")
}

func TestNETCapturer(t *testing.T) {
	n := &miniedr.NETCapturer{}

	got, err := n.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "NETSnapshot(empty)")

	nowCalls := 0
	nowSeq := []time.Time{time.Unix(10, 0), time.Unix(15, 0)}
	n.Now = func() time.Time {
		if nowCalls >= len(nowSeq) {
			return nowSeq[len(nowSeq)-1]
		}
		defer func() { nowCalls++ }()
		return nowSeq[nowCalls]
	}

	ioSeq := [][]gnet.IOCountersStat{
		{
			{Name: "eth0", BytesRecv: 100, BytesSent: 50},
			{Name: "lo", BytesRecv: 10, BytesSent: 5},
		},
		{
			{Name: "eth0", BytesRecv: 200, BytesSent: 80},
			{Name: "lo", BytesRecv: 20, BytesSent: 5},
		},
	}
	ioCall := 0
	n.IOFn = func(pernic bool) ([]gnet.IOCountersStat, error) {
		defer func() { ioCall++ }()
		return ioSeq[ioCall], nil
	}

	assertError(t, n.Capture(), "")
	got, err = n.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "NETSnapshot(at=1970-01-01T09:00:10+09:00, ifaces=2, rxRate=n/a, txRate=n/a)")

	assertError(t, n.Capture(), "")
	got, err = n.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "NETSnapshot(at=1970-01-01T09:00:15+09:00, ifaces=2, rxRate=22B/s, txRate=6B/s)")
}

func TestConnCapturer(t *testing.T) {
	c := &miniedr.ConnCapturer{
		Kind: "all",
	}

	got, err := c.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "ConnSnapshot(empty)")

	nowCalls := 0
	nowSeq := []time.Time{time.Unix(0, 0), time.Unix(5, 0)}
	c.Now = func() time.Time {
		if nowCalls >= len(nowSeq) {
			return nowSeq[len(nowSeq)-1]
		}
		defer func() { nowCalls++ }()
		return nowSeq[nowCalls]
	}

	connSeq := [][]gnet.ConnectionStat{
		{
			{Family: 2, Type: 1, Pid: 10, Status: "LISTEN", Laddr: gnet.Addr{IP: "127.0.0.1", Port: 80}, Raddr: gnet.Addr{}},
			{Family: 2, Type: 2, Pid: 20, Status: "ESTABLISHED", Laddr: gnet.Addr{IP: "10.0.0.1", Port: 1234}, Raddr: gnet.Addr{IP: "1.1.1.1", Port: 443}},
		},
		{
			{Family: 2, Type: 2, Pid: 20, Status: "ESTABLISHED", Laddr: gnet.Addr{IP: "10.0.0.1", Port: 1234}, Raddr: gnet.Addr{IP: "1.1.1.1", Port: 443}},
			{Family: 2, Type: 1, Pid: 30, Status: "ESTABLISHED", Laddr: gnet.Addr{IP: "192.168.1.2", Port: 8080}, Raddr: gnet.Addr{IP: "8.8.8.8", Port: 53}},
		},
	}
	connCall := 0
	c.ConnectionsFn = func(kind string) ([]gnet.ConnectionStat, error) {
		defer func() { connCall++ }()
		return connSeq[connCall], nil
	}

	assertError(t, c.Capture(), "")
	got, err = c.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "ConnSnapshot(at=1970-01-01T09:00:00+09:00, kind=all, conns=2, new=0, dead=0)")

	assertError(t, c.Capture(), "")
	got, err = c.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "ConnSnapshot(at=1970-01-01T09:00:05+09:00, kind=all, conns=2, new=1, dead=1)")
}

func TestDISKCapturer(t *testing.T) {
	d := &miniedr.DISKCapturer{
		Paths: []string{"/mnt"},
	}

	got, err := d.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "DISKSnapshot(empty)")

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

func TestFileWatchCapturer(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write keep: %v", err)
	}

	w := &miniedr.FileWatchCapturer{
		Paths:    []string{dir},
		MaxFiles: 10,
		WalkFn:   filepath.WalkDir,
	}

	got, err := w.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "FileWatchSnapshot(empty)")

	nowCalls := 0
	nowSeq := []time.Time{time.Unix(10, 0), time.Unix(20, 0)}
	w.Now = func() time.Time {
		if nowCalls >= len(nowSeq) {
			return nowSeq[len(nowSeq)-1]
		}
		defer func() { nowCalls++ }()
		return nowSeq[nowCalls]
	}

	assertError(t, w.Capture(), "")

	if err := os.Remove(keep); err != nil {
		t.Fatalf("remove keep: %v", err)
	}
	newPath := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(newPath, []byte("new file"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}

	assertError(t, w.Capture(), "")
	got, err = w.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "FileWatchSnapshot(at=1970-01-01T09:00:20+09:00, files=1, events=2, sample=created:new.txt(+1))")
}

func TestPersistCapturer(t *testing.T) {
	src := &stubPersistSource{
		name: "stub",
		snapshots: []map[string]string{
			{"a": "1", "b": "2"},
			{"a": "1", "c": "3"},
			{"a": "2", "c": "3"},
		},
	}

	p := &miniedr.PersistCapturer{
		Sources: []miniedr.PersistSource{src},
	}

	got, err := p.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "PersistSnapshot(empty)")

	nowCalls := 0
	nowSeq := []time.Time{time.Unix(10, 0), time.Unix(20, 0), time.Unix(30, 0)}
	p.Now = func() time.Time {
		if nowCalls >= len(nowSeq) {
			return nowSeq[len(nowSeq)-1]
		}
		defer func() { nowCalls++ }()
		return nowSeq[nowCalls]
	}

	assertError(t, p.Capture(), "")
	got, err = p.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "PersistSnapshot(at=1970-01-01T09:00:10+09:00, sources=1, added=0, changed=0, removed=0)")

	assertError(t, p.Capture(), "")
	got, err = p.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "PersistSnapshot(at=1970-01-01T09:00:20+09:00, sources=1, added=1, changed=0, removed=1)")

	assertError(t, p.Capture(), "")
	got, err = p.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "PersistSnapshot(at=1970-01-01T09:00:30+09:00, sources=1, added=0, changed=1, removed=0)")
}

func assertError(t testing.TB, got error, want string) {
	t.Helper()
	if got != nil {
		if got.Error() != want {
			t.Errorf("want error %q, got error %q", want, got)
		}
	} else if want != "" {
		t.Errorf("want error %q, got no error", want)
	}
}

func assertEqual[T any](t testing.TB, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want '%+v', got '%+v'", want, got)
	}
}

func assertTrue(t testing.TB, got bool) {
	t.Helper()
	if !got {
		t.Errorf("got %v", got)
	}
}
