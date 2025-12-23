package miniedr

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jaewooli/miniedr/capturer"
)

// IOCConfig defines simple indicator lists for matching.
type IOCConfig struct {
	ProcessNames    []string `json:"process_names,omitempty"`
	ProcessPaths    []string `json:"process_paths,omitempty"`
	ProcessCmdlines []string `json:"process_cmdlines,omitempty"`
	FilePaths       []string `json:"file_paths,omitempty"`
	RemoteIPs       []string `json:"remote_ips,omitempty"`
}

// LoadIOCConfig reads an IOC config from disk.
func LoadIOCConfig(path string) (IOCConfig, error) {
	var cfg IOCConfig
	if path == "" {
		return cfg, fmt.Errorf("ioc config path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read ioc config: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("decode ioc config: %w", err)
	}
	return normalizeIOCConfig(cfg), nil
}

func normalizeIOCConfig(cfg IOCConfig) IOCConfig {
	cfg.ProcessNames = normalizeIndicators(cfg.ProcessNames)
	cfg.ProcessPaths = normalizeIndicators(cfg.ProcessPaths)
	cfg.ProcessCmdlines = normalizeIndicators(cfg.ProcessCmdlines)
	cfg.FilePaths = normalizeIndicators(cfg.FilePaths)
	cfg.RemoteIPs = normalizeIndicators(cfg.RemoteIPs)
	return cfg
}

func normalizeIndicators(vals []string) []string {
	out := make([]string, 0, len(vals))
	seen := make(map[string]struct{}, len(vals))
	for _, v := range vals {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func (c IOCConfig) empty() bool {
	return len(c.ProcessNames) == 0 &&
		len(c.ProcessPaths) == 0 &&
		len(c.ProcessCmdlines) == 0 &&
		len(c.FilePaths) == 0 &&
		len(c.RemoteIPs) == 0
}

// IsEmpty reports whether the config has any indicators.
func (c IOCConfig) IsEmpty() bool {
	return c.empty()
}

// RuleIOCMatch emits alerts when new processes, file events, or connections match IOCs.
func RuleIOCMatch(cfg IOCConfig) RuleSpec {
	cfg = normalizeIOCConfig(cfg)
	m := newIOCMatcher(cfg)
	return RuleSpec{
		ID:       "ioc.match",
		Title:    "IOC match",
		Severity: SeverityHigh,
		Eval: func(info capturer.InfoData) []Alert {
			if m == nil {
				return nil
			}
			var alerts []Alert
			limit := 50

			procs := extractProcMeta(info.Fields)
			for _, p := range procs {
				if indicator, field := m.matchProcess(p); indicator != "" {
					alerts = append(alerts, Alert{
						RuleID:   "ioc.match",
						Message:  fmt.Sprintf("IOC match on process %s (%s=%s)", procLabel(p), field, indicator),
						Evidence: map[string]any{"kind": "process", "indicator": indicator, "field": field, "pid": p.PID, "name": p.Name, "exe": p.Exe, "cmdline": p.Cmdline},
						DedupKey: fmt.Sprintf("ioc|process|%s|%d", indicator, p.PID),
					})
					if len(alerts) >= limit {
						return alerts
					}
				}
			}

			events := extractFileEvents(info.Fields)
			for _, e := range events {
				if indicator := m.matchFile(e.Path); indicator != "" {
					alerts = append(alerts, Alert{
						RuleID:   "ioc.match",
						Message:  fmt.Sprintf("IOC match on file %s (path=%s)", e.Path, indicator),
						Evidence: map[string]any{"kind": "file", "indicator": indicator, "path": e.Path, "event": e.Type},
						DedupKey: fmt.Sprintf("ioc|file|%s|%s", indicator, e.Path),
					})
					if len(alerts) >= limit {
						return alerts
					}
				}
			}

			conns := extractConnIDs(info.Fields)
			for _, c := range conns {
				if indicator := m.matchRemoteIP(c.RIP); indicator != "" {
					alerts = append(alerts, Alert{
						RuleID:   "ioc.match",
						Message:  fmt.Sprintf("IOC match on remote IP %s", c.RIP),
						Evidence: map[string]any{"kind": "connection", "indicator": indicator, "remote_ip": c.RIP, "remote_port": c.RPort, "pid": c.PID},
						DedupKey: fmt.Sprintf("ioc|conn|%s|%s|%d", indicator, c.RIP, c.PID),
					})
					if len(alerts) >= limit {
						return alerts
					}
				}
			}
			return alerts
		},
	}
}

type iocMatcher struct {
	procNames    []string
	procPaths    []string
	procCmdlines []string
	filePaths    []string
	remoteIPs    map[string]struct{}
}

func newIOCMatcher(cfg IOCConfig) *iocMatcher {
	if cfg.empty() {
		return nil
	}
	remoteIPs := make(map[string]struct{}, len(cfg.RemoteIPs))
	for _, ip := range cfg.RemoteIPs {
		remoteIPs[ip] = struct{}{}
	}
	return &iocMatcher{
		procNames:    cfg.ProcessNames,
		procPaths:    cfg.ProcessPaths,
		procCmdlines: cfg.ProcessCmdlines,
		filePaths:    cfg.FilePaths,
		remoteIPs:    remoteIPs,
	}
}

func (m *iocMatcher) matchProcess(p capturer.ProcMeta) (indicator string, field string) {
	name := strings.ToLower(p.Name)
	exe := strings.ToLower(p.Exe)
	cmd := strings.ToLower(p.Cmdline)
	for _, ind := range m.procNames {
		if ind != "" && strings.Contains(name, ind) {
			return ind, "name"
		}
	}
	for _, ind := range m.procPaths {
		if ind != "" && strings.Contains(exe, ind) {
			return ind, "exe"
		}
	}
	for _, ind := range m.procCmdlines {
		if ind != "" && strings.Contains(cmd, ind) {
			return ind, "cmdline"
		}
	}
	return "", ""
}

func (m *iocMatcher) matchFile(path string) string {
	path = strings.ToLower(path)
	for _, ind := range m.filePaths {
		if ind != "" && strings.Contains(path, ind) {
			return ind
		}
	}
	return ""
}

func (m *iocMatcher) matchRemoteIP(ip string) string {
	ip = strings.ToLower(strings.TrimSpace(ip))
	if ip == "" || m.remoteIPs == nil {
		return ""
	}
	if _, ok := m.remoteIPs[ip]; ok {
		return ip
	}
	return ""
}

func extractProcMeta(fields map[string]interface{}) []capturer.ProcMeta {
	if fields == nil {
		return nil
	}
	raw, ok := fields["proc.new"]
	if !ok {
		return nil
	}
	switch val := raw.(type) {
	case []capturer.ProcMeta:
		return val
	case []*capturer.ProcMeta:
		out := make([]capturer.ProcMeta, 0, len(val))
		for _, p := range val {
			if p != nil {
				out = append(out, *p)
			}
		}
		return out
	default:
		return nil
	}
}

func extractFileEvents(fields map[string]interface{}) []capturer.FileEvent {
	if fields == nil {
		return nil
	}
	raw, ok := fields["file.events"]
	if !ok {
		return nil
	}
	switch val := raw.(type) {
	case []capturer.FileEvent:
		return val
	case []*capturer.FileEvent:
		out := make([]capturer.FileEvent, 0, len(val))
		for _, e := range val {
			if e != nil {
				out = append(out, *e)
			}
		}
		return out
	default:
		return nil
	}
}

func extractConnIDs(fields map[string]interface{}) []capturer.ConnID {
	if fields == nil {
		return nil
	}
	raw, ok := fields["conn.new"]
	if !ok {
		return nil
	}
	switch val := raw.(type) {
	case []capturer.ConnID:
		return val
	case []*capturer.ConnID:
		out := make([]capturer.ConnID, 0, len(val))
		for _, c := range val {
			if c != nil {
				out = append(out, *c)
			}
		}
		return out
	default:
		return nil
	}
}

func procLabel(p capturer.ProcMeta) string {
	if p.Name != "" {
		return p.Name
	}
	if p.Exe != "" {
		return p.Exe
	}
	if p.Cmdline != "" {
		return p.Cmdline
	}
	return fmt.Sprintf("pid=%d", p.PID)
}
