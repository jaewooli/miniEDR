package capturer

// Config structures for enabling/disabling capturers and their options.

type CapturerToggle struct {
	Enabled bool `json:"enabled"`
}

type ConnCfg struct {
	Enabled bool   `json:"enabled"`
	Kind    string `json:"kind"` // tcp/udp/all
}

type DiskCfg struct {
	Enabled bool     `json:"enabled"`
	Paths   []string `json:"paths"`
}

type FileChangeCfg struct {
	Enabled  bool     `json:"enabled"`
	Paths    []string `json:"paths"`
	MaxFiles int      `json:"max_files"`
}

type CapturersConfig struct {
	Capturers struct {
		CPU        CapturerToggle `json:"cpu"`
		Conn       ConnCfg        `json:"conn"`
		Disk       DiskCfg        `json:"disk"`
		FileChange FileChangeCfg  `json:"filewatch"`
		MEM        CapturerToggle `json:"mem"`
		NET        CapturerToggle `json:"net"`
		Persist    CapturerToggle `json:"persist"`
		Proc       CapturerToggle `json:"proc"`
	} `json:"capturers"`
}

func defaultCapturersConfig() CapturersConfig {
	var cfg CapturersConfig

	// 기본값: 전부 켬
	cfg.Capturers.CPU.Enabled = true
	cfg.Capturers.Conn.Enabled = true
	cfg.Capturers.Conn.Kind = "all"
	cfg.Capturers.Disk.Enabled = true
	cfg.Capturers.Disk.Paths = []string{"/"}

	cfg.Capturers.FileChange.Enabled = true
	cfg.Capturers.FileChange.Paths = nil // NewFileChangeCapturer() 내부 defaultWatchPaths() 쓰게
	cfg.Capturers.FileChange.MaxFiles = 50_000

	cfg.Capturers.MEM.Enabled = true
	cfg.Capturers.NET.Enabled = true
	cfg.Capturers.Persist.Enabled = true
	cfg.Capturers.Proc.Enabled = true

	return cfg
}
