package miniedr

import (
	"errors"
	"fmt"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
)

type NETSnapshot struct {
	At    time.Time
	PerIF map[string]gnet.IOCountersStat // interface name -> counters
}

type NETCapturer struct {
	Now  func() time.Time
	IOFn func(pernic bool) ([]gnet.IOCountersStat, error) // net.IOCounters

	prev *NETSnapshot
	curr *NETSnapshot
}

func NewNETCapturer() *NETCapturer {
	return &NETCapturer{
		Now:  time.Now,
		IOFn: gnet.IOCounters,
	}
}

func (n *NETCapturer) Capture() error {
	if n.Now == nil {
		n.Now = time.Now
	}
	if n.IOFn == nil {
		return errors.New("net capturer: IOFn is nil")
	}

	list, err := n.IOFn(true)
	if err != nil {
		return fmt.Errorf("net.IOCounters(pernic=true): %w", err)
	}

	m := make(map[string]gnet.IOCountersStat, len(list))
	for _, x := range list {
		m[x.Name] = x
	}

	snap := &NETSnapshot{At: n.Now(), PerIF: m}
	n.prev = n.curr
	n.curr = snap
	return nil
}

func (n *NETCapturer) GetInfo() (string, error) {
	if n.curr == nil {
		return "NETSnapshot(empty)", nil
	}

	// 델타 기반 초당 트래픽(rate) 계산: (BytesDelta / seconds)
	rxRate, txRate := "n/a", "n/a"
	if n.prev != nil {
		sec := n.curr.At.Sub(n.prev.At).Seconds()
		if sec > 0 {
			var rxDelta, txDelta uint64
			for ifname, cur := range n.curr.PerIF {
				prev, ok := n.prev.PerIF[ifname]
				if !ok {
					continue
				}
				if cur.BytesRecv >= prev.BytesRecv {
					rxDelta += cur.BytesRecv - prev.BytesRecv
				}
				if cur.BytesSent >= prev.BytesSent {
					txDelta += cur.BytesSent - prev.BytesSent
				}
			}
			rxRate = fmt.Sprintf("%dB/s", uint64(float64(rxDelta)/sec))
			txRate = fmt.Sprintf("%dB/s", uint64(float64(txDelta)/sec))
		}
	}

	return fmt.Sprintf(
		"NETSnapshot(at=%s, ifaces=%d, rxRate=%s, txRate=%s)",
		n.curr.At.Format(time.RFC3339),
		len(n.curr.PerIF),
		rxRate, txRate,
	), nil
}
