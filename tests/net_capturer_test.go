package miniedr_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/jaewooli/miniedr"
	gnet "github.com/shirou/gopsutil/v4/net"
)

func TestNETCapturer(t *testing.T) {
	n := &miniedr.NETCapturer{}

	got, err := n.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "NETSnapshot(empty)")

	t.Run("error when IOFn nil", func(t *testing.T) {
		n2 := &miniedr.NETCapturer{}
		n2.IOFn = nil
		err := n2.Capture()
		assertError(t, err, "net capturer: IOFn is nil")
	})

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

	t.Run("propagates connection errors", func(t *testing.T) {
		c2 := &miniedr.ConnCapturer{
			Kind:          "all",
			ConnectionsFn: func(kind string) ([]gnet.ConnectionStat, error) { return nil, fmt.Errorf("boom") },
		}
		err := c2.Capture()
		assertError(t, err, "net.Connections(\"all\"): boom")
	})

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

func TestNETCapturerVerbose(t *testing.T) {
	n := &miniedr.NETCapturer{}

	nowSeq := []time.Time{time.Unix(10, 0), time.Unix(15, 0)}
	nowCalls := 0
	n.Now = func() time.Time {
		if nowCalls >= len(nowSeq) {
			return nowSeq[len(nowSeq)-1]
		}
		defer func() { nowCalls++ }()
		return nowSeq[nowCalls]
	}

	ioSeq := [][]gnet.IOCountersStat{
		{
			{Name: "eth0", BytesRecv: 100, BytesSent: 50, PacketsRecv: 10, PacketsSent: 5, Errin: 1, Errout: 2, Dropin: 3, Dropout: 4},
		},
		{
			{Name: "eth0", BytesRecv: 200, BytesSent: 90, PacketsRecv: 30, PacketsSent: 15, Errin: 2, Errout: 3, Dropin: 4, Dropout: 6},
		},
	}
	ioCall := 0
	n.IOFn = func(pernic bool) ([]gnet.IOCountersStat, error) {
		defer func() { ioCall++ }()
		return ioSeq[ioCall], nil
	}

	assertError(t, n.Capture(), "")
	assertError(t, n.Capture(), "")

	got, err := n.GetVerboseInfo()
	assertError(t, err, "")
	want := "" +
		"NETSnapshot(at=1970-01-01T09:00:15+09:00)\n" +
		"- eth0 rx=20B/s tx=8B/s pkts=4.0/2.0 per sec err=1/1 drop=1/2"
	assertEqual(t, got, want)
}

func TestConnCapturerVerbose(t *testing.T) {
	c := &miniedr.ConnCapturer{
		Kind: "all",
	}

	nowSeq := []time.Time{time.Unix(0, 0), time.Unix(5, 0)}
	nowCalls := 0
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
	assertError(t, c.Capture(), "")

	got, err := c.GetVerboseInfo()
	assertError(t, err, "")
	want := "" +
		"ConnSnapshot(at=1970-01-01T09:00:05+09:00, kind=all, total=2)\n" +
		"States: ESTABLISHED=2\n" +
		"New:\n" +
		"- tcp pid=30 192.168.1.2:8080 -> 8.8.8.8:53 status=ESTABLISHED\n" +
		"Closed:\n" +
		"- tcp pid=10 127.0.0.1:80 -> :0 status=LISTEN"
	assertEqual(t, got, want)
}
