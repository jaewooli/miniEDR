package miniedr

import (
	// "github.com/shirou/gopsutil/v4"
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
)

type Info interface {
	GetInfo() (string, error)
}

type Capturer interface {
	Info
	Capture() error
}

type DefaultCapturer struct {
}

type CPUCapturer struct {
	DefaultCapturer
}

type SnapshotManager struct {
	out       io.Writer
	capturers []Capturer
}

// need to keep transaction
func (s *SnapshotManager) Capture() error {

	if len(s.capturers) == 0 {
		return errors.New("no snapshot is in snapshot manager")
	}
	for _, snapshot := range s.capturers {
		err := snapshot.Capture()
		if err != nil {
			errorString := fmt.Sprintf("error in snapshot capturing: %q", err.Error())
			return errors.New(errorString)
		}

	}
	return nil
}

func (s *SnapshotManager) GetInfo() (string, error) {
	info := &bytes.Buffer{}

	fmt.Fprintf(info, "out: %v\ncapturers: [", reflect.TypeOf(s.out))
	infoString := info.String()

	capturersInfo := reflect.ValueOf(s.capturers)

	fmt.Fprintf(info, "%v %d", capturersInfo.Kind(), capturersInfo.Len())
	for i := range capturersInfo.Len() {
		ptrCapturer := capturersInfo.Index(i)
		infoString += fmt.Sprintf("%v", ptrCapturer.Elem())
	}
	infoString += "]"

	return infoString, nil
}

func NewSnapshotManager(out io.Writer, capturers []Capturer) *SnapshotManager {
	return &SnapshotManager{
		out:       out,
		capturers: capturers,
	}
}
