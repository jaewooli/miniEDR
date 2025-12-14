package miniedr_test

import (
	"bytes"
	"testing"

	"github.com/jaewooli/miniedr"
)

type StubCapturer struct {
}

func (s *StubCapturer) GetInfo() (string, error) {
	return "", nil
}
func (s *StubCapturer) Capture() error {
	return nil
}

func TestSnapshot(t *testing.T) {
	out := &bytes.Buffer{}
	capturers := []miniedr.Capturer{}

	t.Run("create new empty SnapshotManager", func(t *testing.T) {
		snapshotManager := miniedr.NewSnapshotManager(out, capturers)

		got, err := snapshotManager.GetInfo()

		assertError(t, err, "")
		assertTrue(t, got, "out: *bytes.Buffer\ncapturers: []")

		t.Run("capture for SnapshotManager", func(t *testing.T) {
			err2 := snapshotManager.Capture()
			assertError(t, err2, "no snapshot is in snapshot manager")
		})

	})

	t.Run("create new not empty SnapshotManager", func(t *testing.T) {
		stubCapturer := StubCapturer{}
		stubAppendedCapturers := append(capturers, &stubCapturer)

		snapshotManager := miniedr.NewSnapshotManager(out, stubAppendedCapturers)
		got, err := snapshotManager.GetInfo()

		assertError(t, err, "")
		assertTrue(t, got, "out: *bytes.Buffer\ncapturers: [&{}]")

		t.Run("capture for SnapshotManager", func(t *testing.T) {
			err2 := snapshotManager.Capture()
			assertError(t, err2, "")
		})
	})
}

func TestMemSnapShot(t *testing.T) {

}

func assertError(t testing.TB, got error, want string) {
	t.Helper()
	if got != nil {
		if got.Error() != want {
			t.Errorf("want error %q, got error %q", want, got)
		}
	} else if want != "" {
		t.Errorf("want error %q, got no error", want)
	}
}

func assertTrue(t testing.TB, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}
