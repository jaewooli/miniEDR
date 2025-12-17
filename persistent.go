package miniedr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

func NewPersistCapturer() *PersistCapturer {
	return &PersistCapturer{
		Now:     time.Now,
		Sources: defaultPersistSources(),
	}
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

// GetVerboseInfo returns per-source changes with sample keys.
func (p *PersistCapturer) GetVerboseInfo() (string, error) {
	if p.curr == nil {
		return "PersistSnapshot(verbose-empty)", nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "PersistSnapshot(at=%s)\n", p.curr.At.Format(time.RFC3339))

	names := make([]string, 0, len(p.curr.Sources))
	for name := range p.curr.Sources {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		added := p.curr.Added[name]
		changed := p.curr.Changed[name]
		removed := p.curr.Removed[name]
		fmt.Fprintf(&b, "- %s entries=%d added=%d changed=%d removed=%d\n",
			name, len(p.curr.Sources[name]), len(added), len(changed), len(removed))

		printKeys := func(label string, keys []string) {
			if len(keys) == 0 {
				return
			}
			limit := min(10, len(keys))
			fmt.Fprintf(&b, "  %s: %s\n", label, strings.Join(keys[:limit], ", "))
			if extra := len(keys) - limit; extra > 0 {
				fmt.Fprintf(&b, "    ... (+%d more)\n", extra)
			}
		}
		printKeys("added", added)
		printKeys("changed", changed)
		printKeys("removed", removed)
	}

	return strings.TrimSuffix(b.String(), "\n"), nil
}

// IsWarm reports whether a previous snapshot exists (needed for diff-based metrics).
func (p *PersistCapturer) IsWarm() bool {
	return p.prev != nil
}

func defaultPersistSources() []PersistSource {
	return []PersistSource{
		NewCrontabSource(),
		NewSystemdUnitSource(),
		NewAutostartDesktopSource(),
	}
}

type CrontabSource struct{}

func NewCrontabSource() PersistSource {
	return &CrontabSource{}
}

func (c *CrontabSource) Name() string {
	return "crontab"
}

func (c *CrontabSource) Snapshot() (map[string]string, error) {
	out := make(map[string]string)

	files := []string{"/etc/crontab"}
	glob, _ := filepath.Glob("/etc/cron.d/*")
	files = append(files, glob...)

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			key := fmt.Sprintf("%s:%d", path, i)
			out[key] = line
		}
	}

	return out, nil
}

type SystemdUnitSource struct{}

func NewSystemdUnitSource() PersistSource {
	return &SystemdUnitSource{}
}

func (s *SystemdUnitSource) Name() string {
	return "systemd"
}

func (s *SystemdUnitSource) Snapshot() (map[string]string, error) {
	out := make(map[string]string)

	files, err := filepath.Glob("/etc/systemd/system/*.service")
	if err != nil {
		return out, nil
	}

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		out[path] = string(data)
	}

	return out, nil
}

type AutostartDesktopSource struct{}

func NewAutostartDesktopSource() PersistSource {
	return &AutostartDesktopSource{}
}

func (a *AutostartDesktopSource) Name() string {
	return "autostart-desktop"
}

func (a *AutostartDesktopSource) Snapshot() (map[string]string, error) {
	out := make(map[string]string)

	home, err := os.UserHomeDir()
	if err != nil {
		return out, nil
	}

	dir := filepath.Join(home, ".config", "autostart")
	files, _ := filepath.Glob(filepath.Join(dir, "*.desktop"))

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		out[path] = string(data)
	}

	return out, nil
}
