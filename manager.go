package miniedr

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

type SnapshotManager struct {
	out       io.Writer
	capturers []Capturer
}

func NewSnapshotManager(out io.Writer, capturers []Capturer) *SnapshotManager {
	return &SnapshotManager{
		out:       out,
		capturers: capturers,
	}
}

func (sm *SnapshotManager) Capture() error {
	if len(sm.capturers) == 0 {
		return errors.New("no capturer is in snapshot manager")
	}

	for i, c := range sm.capturers {
		if err := c.Capture(); err != nil {
			return fmt.Errorf(
				"snapshot manager: capturer[%d](%T) capture failed: %q",
				i, c, err.Error(),
			)
		}
	}
	return nil
}

func (sm *SnapshotManager) GetInfo() (string, error) {
	if len(sm.capturers) == 0 {
		return "SnapshotManager(capturers=0)\n", nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "SnapshotManager(out=%T, capturers=%d)\n", sm.out, len(sm.capturers))

	for i, c := range sm.capturers {
		info, err := c.GetInfo()
		if err != nil {
			return "", fmt.Errorf(
				"snapshot manager: capturer[%d](%T) GetInfo failed: %w",
				i, c, err,
			)
		}
		fmt.Fprintf(&b, "- [%d] %T: %s\n", i, c, info.Summary)
	}

	return b.String(), nil
}
