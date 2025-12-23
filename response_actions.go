package miniedr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AlertFileResponder writes alerts as JSON lines with size-based rotation.
type AlertFileResponder struct {
	Path     string
	MaxBytes int64 // rotate when file size exceeds this (bytes). Zero -> default 5MB.

	mu   sync.Mutex
	f    *os.File
	enc  *json.Encoder
	size int64
}

func NewAlertFileResponder(path string, maxBytes int64) *AlertFileResponder {
	if maxBytes <= 0 {
		maxBytes = 5 * 1024 * 1024
	}
	return &AlertFileResponder{Path: path, MaxBytes: maxBytes}
}

func (r *AlertFileResponder) Name() string { return "alert_file" }

func (r *AlertFileResponder) Handle(alert Alert) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.ensureFile(); err != nil {
		return err
	}
	if err := r.enc.Encode(alert); err != nil {
		return err
	}
	if stat, err := r.f.Stat(); err == nil {
		r.size = stat.Size()
	}
	if r.size >= r.MaxBytes {
		return r.rotate()
	}
	return nil
}

func (r *AlertFileResponder) ensureFile() error {
	if r.f != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o755); err != nil {
		return fmt.Errorf("mkdir for alert file: %w", err)
	}
	f, err := os.OpenFile(r.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open alert file: %w", err)
	}
	r.f = f
	r.enc = json.NewEncoder(f)
	if st, err := f.Stat(); err == nil {
		r.size = st.Size()
	}
	return nil
}

func (r *AlertFileResponder) rotate() error {
	if r.f != nil {
		_ = r.f.Close()
		r.f = nil
	}
	ts := time.Now().UnixMilli()
	rotated := fmt.Sprintf("%s.%d", r.Path, ts)
	if err := os.Rename(r.Path, rotated); err != nil {
		return fmt.Errorf("rotate alert file: %w", err)
	}
	return r.ensureFile()
}

// ProcessKillerResponder attempts to terminate processes referenced by alert evidence.
// It expects "pid" or "pids" in alert.Evidence.
type ProcessKillerResponder struct {
	DryRun bool
	KillFn func(pid int) error
}

func (r *ProcessKillerResponder) Name() string { return "process_kill" }

func (r *ProcessKillerResponder) Handle(alert Alert) error {
	pids := extractAlertPIDs(alert.Evidence)
	if len(pids) == 0 {
		return fmt.Errorf("process_kill: no pid in alert evidence")
	}
	for _, pid := range pids {
		if r.DryRun {
			continue
		}
		if err := r.kill(pid); err != nil {
			return err
		}
	}
	return nil
}

func (r *ProcessKillerResponder) kill(pid int) error {
	if r.KillFn != nil {
		return r.KillFn(pid)
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process %d: %w", pid, err)
	}
	if err := p.Kill(); err != nil {
		return fmt.Errorf("kill process %d: %w", pid, err)
	}
	return nil
}

func extractAlertPIDs(evidence map[string]any) []int {
	if evidence == nil {
		return nil
	}
	var out []int
	if pid, ok := coercePID(evidence["pid"]); ok {
		out = append(out, pid)
	}
	if raw, ok := evidence["pids"]; ok {
		switch val := raw.(type) {
		case []int:
			out = append(out, val...)
		case []int32:
			for _, v := range val {
				out = append(out, int(v))
			}
		case []int64:
			for _, v := range val {
				out = append(out, int(v))
			}
		case []float64:
			for _, v := range val {
				out = append(out, int(v))
			}
		case []interface{}:
			for _, v := range val {
				if pid, ok := coercePID(v); ok {
					out = append(out, pid)
				}
			}
		}
	}
	return out
}

func coercePID(v any) (int, bool) {
	switch val := v.(type) {
	case int:
		return val, true
	case int32:
		return int(val), true
	case int64:
		return int(val), true
	case float64:
		return int(val), true
	default:
		return 0, false
	}
}
