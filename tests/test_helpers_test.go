package miniedr_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jaewooli/miniedr"
)

type StubCapturer struct{}

func (s *StubCapturer) GetInfo() (string, error) { return "", nil }
func (s *StubCapturer) Capture() error           { return nil }

type stubPersistSource struct {
	name      string
	snapshots []map[string]string
	call      int
}

func (s *stubPersistSource) Name() string { return s.name }
func (s *stubPersistSource) Snapshot() (map[string]string, error) {
	if s.call >= len(s.snapshots) {
		return map[string]string{}, nil
	}
	src := s.snapshots[s.call]
	s.call++
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out, nil
}

func assertCapturers(t *testing.T, got, want miniedr.Capturers) {
	t.Helper()
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
				}
			}
		}
	}
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

func assertContains(t testing.TB, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("want substring %q in %q", sub, s)
	}
}
