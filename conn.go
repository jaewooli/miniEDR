package miniedr

import (
	"errors"
	"fmt"
	"strings"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
)

type ConnKey struct {
	Type   string // tcp/udp
	Laddr  string // ip:port
	Raddr  string // ip:port
	PID    int32
	Status string
}

type ConnSnapshot struct {
	At    time.Time
	Conns map[ConnKey]gnet.ConnectionStat
	New   []ConnKey
	Dead  []ConnKey
}

type ConnCapturer struct {
	Now func() time.Time

	// ConnectionsFn returns a list of connections.
	// Typical implementations: gnet.Connections("tcp"), gnet.Connections("udp"), gnet.Connections("all")
	ConnectionsFn func(kind string) ([]gnet.ConnectionStat, error)

	// Kind passed to ConnectionsFn. Default: "all".
	Kind string

	prev *ConnSnapshot
	curr *ConnSnapshot
}

func NewConnCapturer(kind string) *ConnCapturer {
	if kind == "" {
		kind = "all"
	}
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

	snap := &ConnSnapshot{
		At:    c.Now(),
		Conns: make(map[ConnKey]gnet.ConnectionStat, len(list)),
	}
	for _, x := range list {
		k := ConnKey{
			Type:   strings.ToLower(x.Type),
			Laddr:  fmt.Sprintf("%s:%d", x.Laddr.IP, x.Laddr.Port),
			Raddr:  fmt.Sprintf("%s:%d", x.Raddr.IP, x.Raddr.Port),
			PID:    x.Pid,
			Status: x.Status,
		}
		snap.Conns[k] = x
	}

	if c.curr != nil {
		for k := range snap.Conns {
			if _, ok := c.curr.Conns[k]; !ok {
				snap.New = append(snap.New, k)
			}
		}
		for k := range c.curr.Conns {
			if _, ok := snap.Conns[k]; !ok {
				snap.Dead = append(snap.Dead, k)
			}
		}
	}

	c.prev = c.curr
	c.curr = snap
	return nil
}

func (c *ConnCapturer) GetInfo() (string, error) {
	if c.curr == nil {
		return "ConnSnapshot(empty)", nil
	}
	return fmt.Sprintf(
		"ConnSnapshot(at=%s, conns=%d, new=%d, dead=%d)",
		c.curr.At.Format(time.RFC3339),
		len(c.curr.Conns),
		len(c.curr.New),
		len(c.curr.Dead),
	), nil
}
