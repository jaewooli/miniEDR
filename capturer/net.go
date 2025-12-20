package capturer

import (
	"errors"
	"fmt"
	"sort"
	"strings"
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

func (n *NETCapturer) GetInfo() (InfoData, error) {
	if n.curr == nil {
		return InfoData{Summary: "NETSnapshot(empty)"}, nil
	}

	metrics := make(map[string]float64)

	// 델타 기반 초당 트래픽(rate) 계산: (BytesDelta / seconds)
	rxRate, txRate := "0B/s", "0B/s"
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
			rx := float64(rxDelta) / sec
			tx := float64(txDelta) / sec
			rxRate = fmt.Sprintf("%dB/s", uint64(rx))
			txRate = fmt.Sprintf("%dB/s", uint64(tx))
			metrics["net.rx_bytes_per_sec"] = rx
			metrics["net.tx_bytes_per_sec"] = tx
		}
	}

	summary := fmt.Sprintf(
		"NETSnapshot(at=%s, ifaces=%d, rxRate=%s, txRate=%s)",
		n.curr.At.Format(time.RFC3339),
		len(n.curr.PerIF),
		rxRate, txRate,
	)
	if len(metrics) == 0 {
		metrics = nil
	}
	return InfoData{Summary: summary, Metrics: metrics}, nil
}

// GetVerboseInfo returns per-interface traffic including packet/error deltas.
func (n *NETCapturer) GetVerboseInfo() (string, error) {
	if n.curr == nil {
		return "NETSnapshot(verbose-empty)", nil
	}

	var b strings.Builder
	sec := 0.0
	if n.prev != nil {
		sec = n.curr.At.Sub(n.prev.At).Seconds()
	}
	fmt.Fprintf(&b, "NETSnapshot(at=%s", n.curr.At.Format(time.RFC3339))
	if n.prev != nil {
		fmt.Fprintf(&b, ", interval=%s", n.curr.At.Sub(n.prev.At).Round(time.Millisecond))
	}
	fmt.Fprintf(&b, ")\n")

	prevIfaces := 0
	if n.prev != nil {
		prevIfaces = len(n.prev.PerIF)
	}
	fmt.Fprintf(&b, "Interfaces: total=%d", len(n.curr.PerIF))
	if n.prev != nil {
		fmt.Fprintf(&b, " (prev=%d, delta=%+d)", prevIfaces, len(n.curr.PerIF)-prevIfaces)
	}

	var rxRate, txRate float64
	if sec > 0 && n.prev != nil {
		for ifname, cur := range n.curr.PerIF {
			prev := n.prev.PerIF[ifname]
			rxRate += float64(deltaUint64(prev.BytesRecv, cur.BytesRecv)) / sec
			txRate += float64(deltaUint64(prev.BytesSent, cur.BytesSent)) / sec
		}
		fmt.Fprintf(&b, " rxRate=%.0fB/s txRate=%.0fB/s", rxRate, txRate)
	}
	fmt.Fprintf(&b, "\n")

	if n.prev != nil {
		newIf, goneIf := diffKeys(n.prev.PerIF, n.curr.PerIF)
		if len(newIf) > 0 {
			fmt.Fprintf(&b, "New ifaces: %s\n", strings.Join(newIf, ", "))
		}
		if len(goneIf) > 0 {
			fmt.Fprintf(&b, "Removed ifaces: %s\n", strings.Join(goneIf, ", "))
		}
	}

	names := make([]string, 0, len(n.curr.PerIF))
	for name := range n.curr.PerIF {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		cur := n.curr.PerIF[name]
		if sec <= 0 || n.prev == nil {
			fmt.Fprintf(&b, "- %s rx=%dB tx=%dB pkts=%d/%d err=%d/%d drop=%d/%d\n",
				name, cur.BytesRecv, cur.BytesSent, cur.PacketsRecv, cur.PacketsSent,
				cur.Errin, cur.Errout, cur.Dropin, cur.Dropout)
			continue
		}
		prev, ok := n.prev.PerIF[name]
		if !ok {
			fmt.Fprintf(&b, "- %s (no-prev) rx=%dB tx=%dB pkts=%d/%d err=%d/%d drop=%d/%d\n",
				name, cur.BytesRecv, cur.BytesSent, cur.PacketsRecv, cur.PacketsSent,
				cur.Errin, cur.Errout, cur.Dropin, cur.Dropout)
			continue
		}

		rx := deltaUint64(prev.BytesRecv, cur.BytesRecv)
		tx := deltaUint64(prev.BytesSent, cur.BytesSent)
		rxPk := deltaUint64(prev.PacketsRecv, cur.PacketsRecv)
		txPk := deltaUint64(prev.PacketsSent, cur.PacketsSent)
		errIn := deltaUint64(prev.Errin, cur.Errin)
		errOut := deltaUint64(prev.Errout, cur.Errout)
		dropIn := deltaUint64(prev.Dropin, cur.Dropin)
		dropOut := deltaUint64(prev.Dropout, cur.Dropout)

		fmt.Fprintf(&b, "- %s rx=%.0fB/s tx=%.0fB/s pkts=%.1f/%.1f per sec err=%d/%d drop=%d/%d\n",
			name,
			float64(rx)/sec, float64(tx)/sec,
			float64(rxPk)/sec, float64(txPk)/sec,
			errIn, errOut, dropIn, dropOut)
	}

	return strings.TrimSuffix(b.String(), "\n"), nil
}

// IsWarm reports whether a previous snapshot exists (needed for rate deltas).
func (n *NETCapturer) IsWarm() bool {
	return n.prev != nil
}

func diffKeys(prev, cur map[string]gnet.IOCountersStat) (added, removed []string) {
	if prev == nil {
		prev = map[string]gnet.IOCountersStat{}
	}
	if cur == nil {
		cur = map[string]gnet.IOCountersStat{}
	}
	for k := range cur {
		if _, ok := prev[k]; !ok {
			added = append(added, k)
		}
	}
	for k := range prev {
		if _, ok := cur[k]; !ok {
			removed = append(removed, k)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return
}
