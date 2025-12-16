package miniedr_test

import (
	"testing"
	"time"

	"github.com/jaewooli/miniedr"
)

func TestPersistCapturer(t *testing.T) {
	src := &stubPersistSource{
		name: "stub",
		snapshots: []map[string]string{
			{"a": "1", "b": "2"},
			{"a": "1", "c": "3"},
			{"a": "2", "c": "3"},
		},
	}

	p := &miniedr.PersistCapturer{
		Sources: []miniedr.PersistSource{src},
	}

	got, err := p.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "PersistSnapshot(empty)")

	nowCalls := 0
	nowSeq := []time.Time{time.Unix(10, 0), time.Unix(20, 0), time.Unix(30, 0)}
	p.Now = func() time.Time {
		if nowCalls >= len(nowSeq) {
			return nowSeq[len(nowSeq)-1]
		}
		defer func() { nowCalls++ }()
		return nowSeq[nowCalls]
	}

	assertError(t, p.Capture(), "")
	got, err = p.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "PersistSnapshot(at=1970-01-01T09:00:10+09:00, sources=1, added=0, changed=0, removed=0)")

	assertError(t, p.Capture(), "")
	got, err = p.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "PersistSnapshot(at=1970-01-01T09:00:20+09:00, sources=1, added=1, changed=0, removed=1)")

	assertError(t, p.Capture(), "")
	got, err = p.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "PersistSnapshot(at=1970-01-01T09:00:30+09:00, sources=1, added=0, changed=1, removed=0)")

	t.Run("error when sources empty", func(t *testing.T) {
		p2 := &miniedr.PersistCapturer{Sources: nil}
		err := p2.Capture()
		assertError(t, err, "persist capturer: Sources is empty")
	})
}
