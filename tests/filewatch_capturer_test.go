package miniedr_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jaewooli/miniedr"
)

func TestFileWatchCapturer(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write keep: %v", err)
	}

	w := &miniedr.FileWatchCapturer{
		Paths:    []string{dir},
		MaxFiles: 10,
		WalkFn:   filepath.WalkDir,
	}

	got, err := w.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "FileWatchSnapshot(empty)")

	nowCalls := 0
	nowSeq := []time.Time{time.Unix(10, 0), time.Unix(20, 0)}
	w.Now = func() time.Time {
		if nowCalls >= len(nowSeq) {
			return nowSeq[len(nowSeq)-1]
		}
		defer func() { nowCalls++ }()
		return nowSeq[nowCalls]
	}

	assertError(t, w.Capture(), "")

	if err := os.Remove(keep); err != nil {
		t.Fatalf("remove keep: %v", err)
	}
	newPath := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(newPath, []byte("new file"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}

	assertError(t, w.Capture(), "")
	got, err = w.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got, "FileWatchSnapshot(at=1970-01-01T09:00:20+09:00, files=1, events=2, sample=created:new.txt(+1))")

	t.Run("single event sample without suffix", func(t *testing.T) {
		dir2 := t.TempDir()
		w2 := &miniedr.FileWatchCapturer{
			Paths:    []string{dir2},
			MaxFiles: 10,
			WalkFn:   filepath.WalkDir,
		}
		nowCalls := 0
		nowSeq := []time.Time{time.Unix(30, 0), time.Unix(40, 0)}
		w2.Now = func() time.Time {
			if nowCalls >= len(nowSeq) {
				return nowSeq[len(nowSeq)-1]
			}
			defer func() { nowCalls++ }()
			return nowSeq[nowCalls]
		}

		assertError(t, w2.Capture(), "")
		newPath := filepath.Join(dir2, "one.txt")
		if err := os.WriteFile(newPath, []byte("one"), 0o644); err != nil {
			t.Fatalf("write one: %v", err)
		}
		assertError(t, w2.Capture(), "")
		got, err := w2.GetInfo()
		assertError(t, err, "")
		assertEqual(t, got, "FileWatchSnapshot(at=1970-01-01T09:00:40+09:00, files=1, events=1, sample=created:one.txt)")
	})
}
