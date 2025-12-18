package miniedr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jaewooli/miniedr/capturer"
)

// TelemetrySink consumes structured captures for persistence or forwarding.
type TelemetrySink interface {
	Consume(info capturer.InfoData) error
}

// JSONFileSink writes InfoData as line-delimited JSON with simple size-based rotation.
type JSONFileSink struct {
	Path     string
	MaxBytes int64 // rotate when file size exceeds this (bytes). Zero -> default 5MB.

	mu   sync.Mutex
	f    *os.File
	enc  *json.Encoder
	size int64
}

func NewJSONFileSink(path string, maxBytes int64) *JSONFileSink {
	if maxBytes <= 0 {
		maxBytes = 5 * 1024 * 1024
	}
	return &JSONFileSink{Path: path, MaxBytes: maxBytes}
}

func (s *JSONFileSink) Consume(info capturer.InfoData) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureFile(); err != nil {
		return err
	}

	if err := s.enc.Encode(info); err != nil {
		return err
	}
	if stat, err := s.f.Stat(); err == nil {
		s.size = stat.Size()
	}
	if s.size >= s.MaxBytes {
		return s.rotate()
	}
	return nil
}

func (s *JSONFileSink) ensureFile() error {
	if s.f != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return fmt.Errorf("mkdir for sink: %w", err)
	}
	f, err := os.OpenFile(s.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open sink file: %w", err)
	}
	s.f = f
	s.enc = json.NewEncoder(f)
	if st, err := f.Stat(); err == nil {
		s.size = st.Size()
	}
	return nil
}

func (s *JSONFileSink) rotate() error {
	if s.f != nil {
		_ = s.f.Close()
		s.f = nil
	}
	ts := time.Now().UnixMilli()
	rotated := fmt.Sprintf("%s.%d", s.Path, ts)
	if err := os.Rename(s.Path, rotated); err != nil {
		return fmt.Errorf("rotate sink file: %w", err)
	}
	return s.ensureFile()
}
