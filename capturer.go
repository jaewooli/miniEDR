package miniedr

type Info interface {
	GetInfo() (string, error)
}

type Capturer interface {
	Info
	Capture() error
}

type Capturers []Capturer

func NewCapturers() []Capturer {
	return Capturers{
		NewCPUCapturer(),
		NewDISKCapturer(),
		NewMEMCapturer(),
		NewNETCapturer(),
	}
}
