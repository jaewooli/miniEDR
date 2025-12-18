package capturer_test

import (
	"bytes"
	"testing"

	"github.com/jaewooli/miniedr/capturer"
)

func TestSnapshot(t *testing.T) {
	out := &bytes.Buffer{}
	capturers := []capturer.Capturer{}

	t.Run("create new empty SnapshotManager", func(t *testing.T) {
		snapshotManager := capturer.NewSnapshotManager(out, capturers)

		got, err := snapshotManager.GetInfo()

		assertError(t, err, "")
		assertEqual(t, got, "SnapshotManager(capturers=0)\n")

		t.Run("capture for SnapshotManager", func(t *testing.T) {
			err2 := snapshotManager.Capture()
			assertError(t, err2, "no capturer is in snapshot manager")
		})
	})

	t.Run("create new not empty SnapshotManager", func(t *testing.T) {
		stubCapturer := StubCapturer{}
		stubAppendedCapturers := append(capturers, &stubCapturer)

		snapshotManager := capturer.NewSnapshotManager(out, stubAppendedCapturers)
		got, err := snapshotManager.GetInfo()

		assertError(t, err, "")
		assertEqual(t, got, "SnapshotManager(out=*bytes.Buffer, capturers=1)\n- [0] *capturer_test.StubCapturer: \n")

		t.Run("capture for SnapshotManager", func(t *testing.T) {
			err2 := snapshotManager.Capture()
			assertError(t, err2, "")
		})
	})
}
