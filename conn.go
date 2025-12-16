package miniedr

import (
	"errors"
	"fmt"
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

func (c *ConnCapturer) GetInfo() (string, error) {
	if c.curr == nil {
		return "ConnSnapshot(empty)", nil
	}
	return fmt.Sprintf(
		"ConnSnapshot(at=%s, kind=%s, conns=%d, new=%d, dead=%d)",
		c.curr.At.Format(time.RFC3339),
		c.Kind,
		len(c.curr.Conns),
		len(c.curr.New),
		len(c.curr.Dead),
	), nil
}
