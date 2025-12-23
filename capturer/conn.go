package capturer

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
)

// ConnProto는 "표시용" 프로토콜 문자열이야.
// (내부 판정/키 비교는 Type/Family 기반으로 한다)
type ConnProto string

const (
	ProtoTCP     ConnProto = "tcp"
	ProtoUDP     ConnProto = "udp"
	ProtoUnknown ConnProto = "unknown"
)

// protoOf는 ConnectionStat의 Family/Type을 보고,
// 사람이 이해하는 tcp/udp로 변환해주는 "의도 있는" 변환기.
func protoOf(cs gnet.ConnectionStat) ConnProto {
	// gopsutil의 Type은 소켓 타입 (SOCK_STREAM=1, SOCK_DGRAM=2) 값을 주는 경우가 많다.
	// - SOCK_STREAM(1) -> TCP로 간주
	// - SOCK_DGRAM(2)  -> UDP로 간주
	// 다만 OS/권한/종류에 따라 완벽히 보장되진 않으니 unknown도 허용한다.
	switch cs.Type {
	case 1:
		return ProtoTCP
	case 2:
		return ProtoUDP
	default:
		return ProtoUnknown
	}
}

// ConnID는 "diff를 위한 식별자"야.
// 문자열 가공(소문자 변환 같은) 대신, 원본 숫자/구조를 최대한 유지해서 안정성을 높인다.
type ConnID struct {
	Family uint32 // AF_INET(2), AF_INET6(10) 등
	Type   uint32 // SOCK_STREAM(1), SOCK_DGRAM(2) 등
	PID    int32
	Status string

	LIP   string
	LPort uint32
	RIP   string
	RPort uint32
}

type ConnSnapshot struct {
	At    time.Time
	Conns map[ConnID]gnet.ConnectionStat

	New  []ConnID
	Dead []ConnID
}

type ConnCapturer struct {
	Now func() time.Time

	// gnet.Connections("tcp"|"udp"|"all")
	ConnectionsFn func(kind string) ([]gnet.ConnectionStat, error)

	Kind string

	prev *ConnSnapshot
	curr *ConnSnapshot
}

func NewConnCapturer(kind string) *ConnCapturer {
	return &ConnCapturer{
		Now:           time.Now,
		ConnectionsFn: gnet.Connections,
		Kind:          kind,
	}
}

func (c *ConnCapturer) Capture() error {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.ConnectionsFn == nil {
		return errors.New("conn capturer: ConnectionsFn is nil")
	}

	list, err := c.ConnectionsFn(c.Kind)
	if err != nil {
		return fmt.Errorf("net.Connections(%q): %w", c.Kind, err)
	}

	next := &ConnSnapshot{
		At:    c.Now(),
		Conns: make(map[ConnID]gnet.ConnectionStat, len(list)),
	}

	for _, cs := range list {
		id := ConnID{
			Family: cs.Family,
			Type:   cs.Type,
			PID:    cs.Pid,
			Status: cs.Status,

			LIP:   cs.Laddr.IP,
			LPort: cs.Laddr.Port,
			RIP:   cs.Raddr.IP,
			RPort: cs.Raddr.Port,
		}
		next.Conns[id] = cs
	}

	// diff: "이전 스냅샷(prev)"과 "이번 스냅샷(next)"을 비교한다.
	if c.curr != nil {
		prev := c.curr

		// 새로 생긴 것: next에는 있는데 prev에는 없음
		for id := range next.Conns {
			if _, ok := prev.Conns[id]; !ok {
				next.New = append(next.New, id)
			}
		}

		// 사라진 것: prev에는 있는데 next에는 없음
		for id := range prev.Conns {
			if _, ok := next.Conns[id]; !ok {
				next.Dead = append(next.Dead, id)
			}
		}
	}

	c.prev = c.curr
	c.curr = next
	return nil
}

func (c *ConnCapturer) GetInfo() (InfoData, error) {
	if c.curr == nil {
		return InfoData{Summary: "ConnSnapshot(empty)"}, nil
	}
	metrics := map[string]float64{
		"conn.total": float64(len(c.curr.Conns)),
		"conn.new":   float64(len(c.curr.New)),
		"conn.dead":  float64(len(c.curr.Dead)),
	}
	summary := fmt.Sprintf(
		"ConnSnapshot(at=%s, kind=%s, conns=%d, new=%d, dead=%d)",
		c.curr.At.Format(time.RFC3339),
		c.Kind,
		len(c.curr.Conns),
		len(c.curr.New),
		len(c.curr.Dead),
	)
	var fields map[string]interface{}
	if len(c.curr.New) > 0 {
		limit := min(200, len(c.curr.New))
		conns := make([]ConnID, 0, limit)
		for i := 0; i < limit; i++ {
			conns = append(conns, c.curr.New[i])
		}
		fields = map[string]interface{}{
			"conn.new": conns,
		}
	}
	return InfoData{Summary: summary, Metrics: metrics, Fields: fields}, nil
}

// GetVerboseInfo returns connection states and samples of new/dead entries.
func (c *ConnCapturer) GetVerboseInfo() (string, error) {
	if c.curr == nil {
		return "ConnSnapshot(verbose-empty)", nil
	}

	var b strings.Builder
	prevTotal := 0
	if c.prev != nil {
		prevTotal = len(c.prev.Conns)
	}
	delta := len(c.curr.Conns) - prevTotal
	fmt.Fprintf(&b, "ConnSnapshot(at=%s, kind=%s, total=%d", c.curr.At.Format(time.RFC3339), c.Kind, len(c.curr.Conns))
	if c.prev != nil {
		fmt.Fprintf(&b, ", prev=%d, delta=%+d", prevTotal, delta)
	}
	fmt.Fprintf(&b, ")\n")
	fmt.Fprintf(&b, "Churn: new=%d dead=%d\n", len(c.curr.New), len(c.curr.Dead))

	// State distribution
	if len(c.curr.Conns) > 0 {
		currStates := countConnStates(c.curr.Conns)
		prevStates := countConnStates(c.prevConns())
		stateLines := formatCountDelta(currStates, prevStates)
		if len(stateLines) > 0 {
			fmt.Fprintf(&b, "States: %s\n", strings.Join(stateLines, " "))
		}
	}

	// Protocol distribution
	if len(c.curr.Conns) > 0 {
		currProto := countConnProtos(c.curr.Conns)
		prevProto := countConnProtos(c.prevConns())
		protoLines := formatProtoDelta(currProto, prevProto)
		if len(protoLines) > 0 {
			fmt.Fprintf(&b, "Protocols: %s\n", strings.Join(protoLines, " "))
		}
	}

	if len(c.curr.New) > 0 {
		fmt.Fprintf(&b, "New:\n")
		lines := formatConnIDs(c.curr.New, c.curr.Conns, 10)
		for _, ln := range lines {
			fmt.Fprintf(&b, "- %s\n", ln)
		}
		if extra := len(c.curr.New) - len(lines); extra > 0 {
			fmt.Fprintf(&b, "  ... (+%d more)\n", extra)
		}
	}

	if len(c.curr.Dead) > 0 {
		fmt.Fprintf(&b, "Closed:\n")
		lines := formatConnIDs(c.curr.Dead, c.prevConns(), 10)
		for _, ln := range lines {
			fmt.Fprintf(&b, "- %s\n", ln)
		}
		if extra := len(c.curr.Dead) - len(lines); extra > 0 {
			fmt.Fprintf(&b, "  ... (+%d more)\n", extra)
		}
	}

	return strings.TrimSuffix(b.String(), "\n"), nil
}

// IsWarm reports whether a previous snapshot exists (needed for new/dead deltas).
func (c *ConnCapturer) IsWarm() bool {
	return c.prev != nil
}

func formatConnIDs(ids []ConnID, source map[ConnID]gnet.ConnectionStat, limit int) []string {
	n := len(ids)
	if limit > 0 && n > limit {
		n = limit
	}
	list := make([]string, 0, n)

	// Stable order: sort by string form of id
	sorted := make([]string, 0, len(ids))
	idMap := make(map[string]ConnID, len(ids))
	for _, id := range ids {
		key := connIDKey(id)
		sorted = append(sorted, key)
		idMap[key] = id
	}
	sort.Strings(sorted)
	if limit > 0 && len(sorted) > limit {
		sorted = sorted[:limit]
	}

	for _, key := range sorted {
		id := idMap[key]
		cs := source[id]
		proto := connProtoFromType(id.Type)
		fmtStr := "%s pid=%d %s:%d -> %s:%d status=%s"
		list = append(list, fmt.Sprintf(fmtStr, proto, id.PID, id.LIP, id.LPort, id.RIP, id.RPort, cs.Status))
	}
	return list
}

func connIDKey(id ConnID) string {
	return fmt.Sprintf("%d|%d|%d|%s|%s|%d|%s|%d", id.Family, id.Type, id.PID, id.Status, id.LIP, id.LPort, id.RIP, id.RPort)
}

func countConnStates(conns map[ConnID]gnet.ConnectionStat) map[string]int {
	if len(conns) == 0 {
		return nil
	}
	out := make(map[string]int)
	for _, cs := range conns {
		out[cs.Status]++
	}
	return out
}

func countConnProtos(conns map[ConnID]gnet.ConnectionStat) map[ConnProto]int {
	if len(conns) == 0 {
		return nil
	}
	out := make(map[ConnProto]int)
	for _, cs := range conns {
		out[connProtoFromType(cs.Type)]++
	}
	return out
}

func formatCountDelta(cur map[string]int, prev map[string]int) []string {
	if len(cur) == 0 {
		return nil
	}
	var prevCounts map[string]int
	if prev != nil {
		prevCounts = prev
	}
	var out []string
	keys := make([]string, 0, len(cur))
	for k := range cur {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		curVal := cur[k]
		line := fmt.Sprintf("%s=%d", k, curVal)
		if prevCounts != nil {
			prevVal := prevCounts[k]
			if d := curVal - prevVal; d != 0 {
				line = fmt.Sprintf("%s=%d(%+d)", k, curVal, d)
			}
		}
		out = append(out, line)
	}
	return out
}

func formatProtoDelta(cur map[ConnProto]int, prev map[ConnProto]int) []string {
	if len(cur) == 0 {
		return nil
	}
	var prevCounts map[ConnProto]int
	if prev != nil {
		prevCounts = prev
	}
	var out []string
	keys := make([]ConnProto, 0, len(cur))
	for k := range cur {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		curVal := cur[k]
		line := fmt.Sprintf("%s=%d", k, curVal)
		if prevCounts != nil {
			if d := curVal - prevCounts[k]; d != 0 {
				line = fmt.Sprintf("%s=%d(%+d)", k, curVal, d)
			}
		}
		out = append(out, line)
	}
	return out
}

func connProtoFromType(t uint32) ConnProto {
	switch t {
	case 1:
		return ProtoTCP
	case 2:
		return ProtoUDP
	default:
		return ProtoUnknown
	}
}

func (c *ConnCapturer) prevConns() map[ConnID]gnet.ConnectionStat {
	if c.prev != nil {
		return c.prev.Conns
	}
	return nil
}
