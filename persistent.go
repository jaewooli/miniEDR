package miniedr

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

type PersistSource interface {
	Name() string
	Snapshot() (map[string]string, error)
}

type PersistSnapshot struct {
	At      time.Time
	Sources map[string]map[string]string // source -> entries

	Added   map[string][]string // source -> keys
	Removed map[string][]string // source -> keys
	Changed map[string][]string // source -> keys
}

// PersistCapturer does NOT implement OS-specific logic by itself.
// Instead, you inject one or more PersistSource implementations.
type PersistCapturer struct {
	Now     func() time.Time
	Sources []PersistSource

	prev *PersistSnapshot
	curr *PersistSnapshot
}

func NewPersistCapturer(sources ...PersistSource) *PersistCapturer {
	return &PersistCapturer{Now: time.Now, Sources: sources}
}

func (p *PersistCapturer) Capture() error {
	if p.Now == nil {
		p.Now = time.Now
	}
	if len(p.Sources) == 0 {
		return errors.New("persist capturer: Sources is empty")
	}

	snap := &PersistSnapshot{
		At:      p.Now(),
		Sources: make(map[string]map[string]string, len(p.Sources)),
		Added:   make(map[string][]string),
		Removed: make(map[string][]string),
		Changed: make(map[string][]string),
	}

	for _, src := range p.Sources {
		if src == nil {
			continue
		}
		m, err := src.Snapshot()
		if err != nil {
			return fmt.Errorf("persist source %q snapshot: %w", src.Name(), err)
		}
		if m == nil {
			m = map[string]string{}
		}
		snap.Sources[src.Name()] = m
	}

	// Diff per source
	if p.curr != nil {
		for name, cur := range snap.Sources {
			prev := p.curr.Sources[name]
			// added/changed
			for k, v := range cur {
				pv, ok := prev[k]
				if !ok {
					snap.Added[name] = append(snap.Added[name], k)
					continue
				}
				if pv != v {
					snap.Changed[name] = append(snap.Changed[name], k)
				}
			}
			// removed
			for k := range prev {
				if _, ok := cur[k]; !ok {
					snap.Removed[name] = append(snap.Removed[name], k)
				}
			}
			sort.Strings(snap.Added[name])
			sort.Strings(snap.Changed[name])
			sort.Strings(snap.Removed[name])
		}
	}

	p.prev = p.curr
	p.curr = snap
	return nil
}

func (p *PersistCapturer) GetInfo() (string, error) {
	if p.curr == nil {
		return "PersistSnapshot(empty)", nil
	}
	sources := len(p.curr.Sources)
	var added, changed, removed int
	for _, ks := range p.curr.Added {
		added += len(ks)
	}
	for _, ks := range p.curr.Changed {
		changed += len(ks)
	}
	for _, ks := range p.curr.Removed {
		removed += len(ks)
	}
	return fmt.Sprintf(
		"PersistSnapshot(at=%s, sources=%d, added=%d, changed=%d, removed=%d)",
		p.curr.At.Format(time.RFC3339), sources, added, changed, removed,
	), nil
}
