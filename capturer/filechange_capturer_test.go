package capturer_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jaewooli/miniedr/capturer"
)

func TestFileChangeCapturer(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write keep: %v", err)
	}

	w := &capturer.FileChangeCapturer{
		Paths:    []string{dir},
		MaxFiles: 10,
		WalkFn:   filepath.WalkDir,
	}

	got, err := w.GetInfo()
	assertError(t, err, "")
	assertEqual(t, got.Summary, "FileChangeSnapshot(empty)")

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
	assertEqual(t, got.Summary, "FileChangeSnapshot(at=1970-01-01T09:00:20+09:00, files=1, events=2, sample=created:new.txt(+1))")

	t.Run("error when paths empty", func(t *testing.T) {
		fw := &capturer.FileChangeCapturer{Paths: nil, WalkFn: filepath.WalkDir}
		err := fw.Capture()
		assertError(t, err, "filewatch capturer: Paths is empty")
	})

	t.Run("respects max files cutoff", func(t *testing.T) {
		dir3 := t.TempDir()
		for i := 0; i < 5; i++ {
			_ = os.WriteFile(filepath.Join(dir3, fmt.Sprintf("f%d.txt", i)), []byte("x"), 0o644)
		}
		scanned := 0
		fw := &capturer.FileChangeCapturer{
			Paths:    []string{dir3},
			MaxFiles: 2,
			WalkFn: func(root string, fn fs.WalkDirFunc) error {
				return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
					if err != nil {
						return nil
					}
					if d.IsDir() {
						return nil
					}
					scanned++
					return fn(path, d, err)
				})
			},
		}
		_ = fw.Capture()
		if scanned < fw.MaxFiles {
			t.Fatalf("expected scan to stop after hitting max files; scanned=%d", scanned)
		}
	})

	t.Run("single event sample without suffix", func(t *testing.T) {
		dir2 := t.TempDir()
		w2 := &capturer.FileChangeCapturer{
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
		assertEqual(t, got.Summary, "FileChangeSnapshot(at=1970-01-01T09:00:40+09:00, files=1, events=1, sample=created:one.txt)")
	})
}

func TestFileChangeCapturerVerbose(t *testing.T) {
	nowSeq := []time.Time{time.Unix(10, 0), time.Unix(20, 0)}
	nowCalls := 0
	filesSeq := []map[string]capturer.FileMeta{
		{
			"/root/a.txt": {Size: 1, Mode: 0o644, ModTime: time.Unix(5, 0)},
		},
		{
			"/root/a.txt": {Size: 2, Mode: 0o644, ModTime: time.Unix(6, 0)},
			"/root/b.txt": {Size: 3, Mode: 0o600, ModTime: time.Unix(7, 0)},
		},
	}
	call := 0
	walkFn := func(root string, fn fs.WalkDirFunc) error {
		cur := filesSeq[call]
		for path, meta := range cur {
			entry := stubDirEntry{info: stubFileInfo{
				name:    filepath.Base(path),
				size:    meta.Size,
				mode:    meta.Mode,
				modTime: meta.ModTime,
			}}
			if err := fn(path, entry, nil); err != nil && err != fs.SkipAll {
				return err
			}
		}
		call++
		return nil
	}

	w := &capturer.FileChangeCapturer{
		Paths: []string{"/root"},
		Now: func() time.Time {
			if nowCalls >= len(nowSeq) {
				return nowSeq[len(nowSeq)-1]
			}
			defer func() { nowCalls++ }()
			return nowSeq[nowCalls]
		},
		MaxFiles:   10,
		Extensions: []string{".txt"},
		WalkFn:     walkFn,
	}

	assertError(t, w.Capture(), "")
	assertError(t, w.Capture(), "")

	got, err := w.GetVerboseInfo()
	assertError(t, err, "")
	want := "" +
		"FileChangeSnapshot(at=1970-01-01T09:00:20+09:00, paths=1, files=2, events=2, maxFiles=10)\n" +
		"Roots: /root\n" +
		"Extensions: .txt\n" +
		"Events:\n" +
		"- created /root/b.txt size=3 mode=-rw------- mtime=1970-01-01T09:00:07+09:00\n" +
		"- modified /root/a.txt size=2 mode=-rw-r--r-- mtime=1970-01-01T09:00:06+09:00"
	assertEqual(t, got, want)
}

type stubDirEntry struct {
	info stubFileInfo
}

func (d stubDirEntry) Name() string               { return d.info.name }
func (d stubDirEntry) IsDir() bool                { return false }
func (d stubDirEntry) Type() fs.FileMode          { return d.info.mode.Type() }
func (d stubDirEntry) Info() (fs.FileInfo, error) { return d.info, nil }

type stubFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
}

func (i stubFileInfo) Name() string       { return i.name }
func (i stubFileInfo) Size() int64        { return i.size }
func (i stubFileInfo) Mode() fs.FileMode  { return i.mode }
func (i stubFileInfo) ModTime() time.Time { return i.modTime }
func (i stubFileInfo) IsDir() bool        { return false }
func (i stubFileInfo) Sys() any           { return nil }
