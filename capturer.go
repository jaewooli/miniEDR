package miniedr

import (
	"errors"
	"gopkg.in/yaml.v3"
	"strings"
)

type Info interface {
	GetInfo() (string, error)
}

type Capturer interface {
	Info
	Capture() error
}

type Capturers []Capturer

type CapturersBuilder struct {
	config []string
}

func (cb *CapturersBuilder) SetConfig(configs ...string) {
	cb.config = configs
}

type CapturerToggle struct {
	Enabled bool `yaml:"enabled"`
}

type ConnCfg struct {
	Enabled bool   `yaml:"enabled"`
	Kind    string `yaml:"kind"` // tcp/udp/all
}

type DiskCfg struct {
	Enabled bool     `yaml:"enabled"`
	Paths   []string `yaml:"paths"`
}

type FileWatchCfg struct {
	Enabled  bool     `yaml:"enabled"`
	Paths    []string `yaml:"paths"`
	MaxFiles int      `yaml:"max_files"`
}

type CapturersConfig struct {
	Capturers struct {
		CPU       CapturerToggle `yaml:"cpu"`
		Conn      ConnCfg        `yaml:"conn"`
		Disk      DiskCfg        `yaml:"disk"`
		FileWatch FileWatchCfg   `yaml:"filewatch"`
		MEM       CapturerToggle `yaml:"mem"`
		NET       CapturerToggle `yaml:"net"`
		Persist   CapturerToggle `yaml:"persist"`
		Proc      CapturerToggle `yaml:"proc"`
	} `yaml:"capturers"`
}

func defaultCapturersConfig() CapturersConfig {
	var cfg CapturersConfig

	// 기본값: 전부 켬 (원하면 기본값을 “최소만”으로 바꿔도 됨)
	cfg.Capturers.CPU.Enabled = true
	cfg.Capturers.Conn.Enabled = true
	cfg.Capturers.Conn.Kind = "all"
	cfg.Capturers.Disk.Enabled = true
	cfg.Capturers.Disk.Paths = []string{"/"}

	cfg.Capturers.FileWatch.Enabled = true
	cfg.Capturers.FileWatch.Paths = nil // NewFileWatchCapturer() 내부 defaultWatchPaths() 쓰게
	cfg.Capturers.FileWatch.MaxFiles = 50_000

	cfg.Capturers.MEM.Enabled = true
	cfg.Capturers.NET.Enabled = true
	cfg.Capturers.Persist.Enabled = true
	cfg.Capturers.Proc.Enabled = true

	return cfg
}

func NewCapturersBuilder() *CapturersBuilder {
	return &CapturersBuilder{}
}

func (cb *CapturersBuilder) Build() (Capturers, error) {
	cfg := defaultCapturersConfig()

	// YAML이 들어오면 기본값 위에 덮어쓰기
	if len(cb.config) > 0 {
		raw := strings.Join(cb.config, "\n")
		if strings.TrimSpace(raw) != "" {
			if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
				return nil, err
			}
		}
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
		// 네 코드 베이스에 맞게: NewConnCapturer(kind) 또는 NewConnCapturer() + c.Kind=kind
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
