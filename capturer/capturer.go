package capturer

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// InfoData carries both human readable text and structured metrics to avoid downstream string parsing.
// Summary should remain concise and stable for display/logging.
type Info interface {
	GetInfo() (InfoData, error)
}

// VerboseInfo can be implemented by capturers that can emit additional detail.
type VerboseInfo interface {
	GetVerboseInfo() (string, error)
}

type Capturer interface {
	Info
	Capture() error
}

type Capturers []Capturer

type CapturersBuilder struct {
	config     []string
	configFile string
}

func (cb *CapturersBuilder) SetConfig(configs ...string) {
	cb.config = configs
}

func (cb *CapturersBuilder) SetConfigFile(path string) {
	cb.configFile = path
}

func NewCapturersBuilder() *CapturersBuilder {
	return &CapturersBuilder{}
}

func (cb *CapturersBuilder) Build() (Capturers, error) {
	cfg, err := cb.loadConfig()
	if err != nil {
		return nil, err
	}

	var out Capturers

	// CPU
	if cfg.Capturers.CPU.Enabled {
		out = append(out, NewCPUCapturer())
	}

	// Conn
	if cfg.Capturers.Conn.Enabled {
		kind := cfg.Capturers.Conn.Kind
		if kind == "" {
			kind = "all"
		}

		cc := NewConnCapturer(kind)
		out = append(out, cc)
	}

	// Disk
	if cfg.Capturers.Disk.Enabled {
		paths := cfg.Capturers.Disk.Paths
		if len(paths) == 0 {
			paths = []string{"/"}
		}
		for _, p := range paths {
			out = append(out, NewDISKCapturer(p))
		}
	}

	// FileWatch
	if cfg.Capturers.FileWatch.Enabled {
		fw := NewFileWatchCapturer() // 인자 없는 기본 생성자 (defaultWatchPaths 내부 사용)
		if len(cfg.Capturers.FileWatch.Paths) > 0 {
			fw.Paths = cfg.Capturers.FileWatch.Paths
		}
		if cfg.Capturers.FileWatch.MaxFiles > 0 {
			fw.MaxFiles = cfg.Capturers.FileWatch.MaxFiles
		}
		out = append(out, fw)
	}

	// MEM
	if cfg.Capturers.MEM.Enabled {
		out = append(out, NewMEMCapturer())
	}

	// NET
	if cfg.Capturers.NET.Enabled {
		out = append(out, NewNETCapturer())
	}

	// Persist
	if cfg.Capturers.Persist.Enabled {
		out = append(out, NewPersistCapturer())
	}

	// Proc
	if cfg.Capturers.Proc.Enabled {
		out = append(out, NewProcCapturer())
	}

	if len(out) == 0 {
		return nil, errors.New("no capturers enabled by config")
	}
	return out, nil
}

func (cb *CapturersBuilder) loadConfig() (CapturersConfig, error) {
	cfg := defaultCapturersConfig()

	raw, err := cb.rawConfig()
	if err != nil {
		return cfg, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return cfg, nil
	}

	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}

func (cb *CapturersBuilder) rawConfig() (string, error) {
	switch {
	case len(cb.config) > 0:
		return strings.Join(cb.config, "\n"), nil
	case cb.configFile != "":
		data, err := os.ReadFile(cb.configFile)
		if err != nil {
			return "", fmt.Errorf("read config file %q: %w", cb.configFile, err)
		}
		return string(data), nil
	}
	return "", nil
}
