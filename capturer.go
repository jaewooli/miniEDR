package miniedr

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
func (cb *CapturersBuilder) Build() Capturers {
	return Capturers{
		NewCPUCapturer(),
		NewConnCapturer(),
		NewDISKCapturer(),
		NewFileWatchCapturer(),
		NewMEMCapturer(),
		NewNETCapturer(),
		NewPersistCapturer(),
		NewProcCapturer(),
	}
}

func NewCapturersBuilder() *CapturersBuilder {
	return &CapturersBuilder{}
}
