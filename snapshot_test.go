package miniedr_test

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jaewooli/miniedr"
	"github.com/shirou/gopsutil/v4/mem"
)

type StubCapturer struct {
}

func (s *StubCapturer) GetInfo() (string, error) {
	return "", nil
}
func (s *StubCapturer) Capture() error {
	return nil
}

const (
	memSnapshotText = "MEMSnapshot(at=1970-01-01T09:02:03+09:00, RAM: Total=1000B Avail=250B UsedApprox=750B (75.00%), Free=100B Buffers=50B Cached=200B; Swap: Total=400B Used=100B (25.00%) Free=300B, Sin=1B Sout=2B)"
)

func TestSnapshot(t *testing.T) {
	out := &bytes.Buffer{}
	capturers := []miniedr.Capturer{}

	t.Run("create new empty SnapshotManager", func(t *testing.T) {
		snapshotManager := miniedr.NewSnapshotManager(out, capturers)

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

		snapshotManager := miniedr.NewSnapshotManager(out, stubAppendedCapturers)
		got, err := snapshotManager.GetInfo()

		assertError(t, err, "")
		assertEqual(t, got, "SnapshotManager(out=*bytes.Buffer, capturers=1)\n- [0] *miniedr_test.StubCapturer: \n")

		t.Run("capture for SnapshotManager", func(t *testing.T) {
			err2 := snapshotManager.Capture()
			assertError(t, err2, "")
		})
	})
}

func TestCapturersBuilder(t *testing.T) {
	gotBuilder := miniedr.NewCapturersBuilder()
	got := gotBuilder.Build()

	want := []miniedr.Capturer{
		miniedr.NewCPUCapturer(),
		miniedr.NewConnCapturer(),
		miniedr.NewDISKCapturer(),
		miniedr.NewFileWatchCapturer(),
		miniedr.NewMEMCapturer(),
		miniedr.NewNETCapturer(),
		miniedr.NewPersistCapturer(),
		miniedr.NewProcCapturer(),
	}

	for i, capturer := range got {
		capturerVal := reflect.ValueOf(capturer).Elem()
		wantVal := reflect.ValueOf(want[i]).Elem()

		if capturerVal.Type() != wantVal.Type() {
			t.Errorf("want %+v, got %+v", capturer, want[i])
		}

		for i := 0; i < capturerVal.NumField(); i++ {
			gotField := capturerVal.Field(i)
			wantField := capturerVal.Field(i)

			if gotField.Kind() == reflect.Func {
				assertEqual(t, fmt.Sprintf("%v", gotField), fmt.Sprintf("%v", wantField))
			} else if gotField.Kind() == reflect.Pointer {
				if !(gotField.IsNil() && wantField.IsNil()) {
					t.Errorf("want %+v, got  %+v", wantField, gotField)
				}
			} else {
				if gotField.CanInterface() {

					assertEqual(t, capturerVal.Field(i).Interface(), wantVal.Field(i).Interface())
				} else {
					fmt.Printf("%+v", gotField)
				}
			}
		}
	}

}

func TestMemSnapShot(t *testing.T) {
	memCapturer := &miniedr.MEMCapturer{}

	memCapturer.Now = func() time.Time { return time.Unix(123, 0) }
	memCapturer.VirtualFn = func() (*mem.VirtualMemoryStat, error) {
		return &mem.VirtualMemoryStat{
			Total:     1000,
			Available: 250,
			Free:      100,
			Buffers:   50,
			Cached:    200,
		}, nil
	}

	memCapturer.SwapFn = func() (*mem.SwapMemoryStat, error) {
		return &mem.SwapMemoryStat{
			Total: 400,
			Used:  100,
			Free:  300,
			Sin:   1,
			Sout:  2,
		}, nil
	}

	t.Run("getinfo empty memory snapshot", func(t *testing.T) {
		got, err := memCapturer.GetInfo()
		assertError(t, err, "")
		assertEqual(t, got, "MEMSnapshot(empty)")

	})

	t.Run("getinfo not empty memory snapshot", func(t *testing.T) {
		err := memCapturer.Capture()

		assertError(t, err, "")

		got, err := memCapturer.GetInfo()

		assertError(t, err, "")
		assertEqual(t, got, memSnapshotText)
	})
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

func assertEqual[T any](t testing.TB, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want '%+v', got '%+v'", want, got)
	}
}

func assertTrue(t testing.TB, got bool) {
	t.Helper()
	if !got {
		t.Errorf("got %v", got)
	}
}
