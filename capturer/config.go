package capturer

// Config structures for enabling/disabling capturers and their options.

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

	// 기본값: 전부 켬
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
