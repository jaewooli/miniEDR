package miniedr

type Info interface {
	GetInfo() (string, error)
}

type Capturer interface {
	Info
	Capture() error
}
