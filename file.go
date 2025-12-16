package miniedr

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type FileMeta struct {
	Size    int64
	Mode    fs.FileMode
	ModTime time.Time
}

type FileEventType string

const (
	FileCreated  FileEventType = "created"
	FileModified FileEventType = "modified"
	FileDeleted  FileEventType = "deleted"
)

type FileEvent struct {
	At   time.Time
	Type FileEventType
	Path string
	Meta *FileMeta
}

type FileWatchSnapshot struct {
	At    time.Time
	Files map[string]FileMeta

	Events []FileEvent
}

// FileWatchCapturer polls one or more directories/files and produces a small diff.
// This is intentionally lightweight and dependency-free (no fsnotify).
type FileWatchCapturer struct {
	Now func() time.Time

	Paths []string

	// Extensions filters which files are tracked. If empty, tracks all files.
	// Example: []string{".exe", ".dll", ".ps1", ".sh"}
	Extensions []string

	// MaxFiles caps scanning to avoid accidental heavy walks.
	// If 0, defaults to 50_000.
	MaxFiles int

	// WalkFn is filepath.WalkDir by default.
	WalkFn func(root string, fn fs.WalkDirFunc) error

	prev *FileWatchSnapshot
	curr *FileWatchSnapshot
}

func NewFileWatchCapturer(paths ...string) *FileWatchCapturer {
	return &FileWatchCapturer{
		Now:      time.Now,
		Paths:    defaultWatchPaths(),
		MaxFiles: 50_000,
		WalkFn:   filepath.WalkDir,
	}
}

func (w *FileWatchCapturer) Capture() error {
	if w.Now == nil {
		w.Now = time.Now
	}
	if w.WalkFn == nil {
		return errors.New("filewatch capturer: WalkFn is nil")
	}
	if len(w.Paths) == 0 {
		return errors.New("filewatch capturer: Paths is empty")
	}
	if w.MaxFiles <= 0 {
		w.MaxFiles = 50_000
	}

	extAllow := make(map[string]struct{}, len(w.Extensions))
	for _, e := range w.Extensions {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		extAllow[e] = struct{}{}
	}
	accept := func(path string) bool {
		if len(extAllow) == 0 {
			return true
		}
		ext := strings.ToLower(filepath.Ext(path))
		_, ok := extAllow[ext]
		return ok
	}

	snap := &FileWatchSnapshot{
		At:    w.Now(),
		Files: make(map[string]FileMeta),
	}

	scanned := 0
	for _, root := range w.Paths {
		root := root
		// If a single file was provided, treat it as a root.
		info, err := os.Lstat(root)
		if err == nil && !info.IsDir() {
			if accept(root) {
				snap.Files[root] = FileMeta{Size: info.Size(), Mode: info.Mode(), ModTime: info.ModTime()}
			}
			continue
		}

		err = w.WalkFn(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// best-effort: skip unreadable entries
				return nil
			}
			if d.IsDir() {
				return nil
			}
			scanned++
			if scanned > w.MaxFiles {
				return fs.SkipAll
			}
			if !accept(path) {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			snap.Files[path] = FileMeta{Size: fi.Size(), Mode: fi.Mode(), ModTime: fi.ModTime()}
			return nil
		})
		if err != nil && !errors.Is(err, fs.SkipAll) {
			return fmt.Errorf("filewatch walk(%q): %w", root, err)
		}
	}

	// Diff to events
	if w.curr != nil {
		// created/modified
		for path, cur := range snap.Files {
			prev, ok := w.curr.Files[path]
			if !ok {
				snap.Events = append(snap.Events, FileEvent{At: snap.At, Type: FileCreated, Path: path, Meta: &cur})
				continue
			}
			if cur.Size != prev.Size || !cur.ModTime.Equal(prev.ModTime) || cur.Mode != prev.Mode {
				snap.Events = append(snap.Events, FileEvent{At: snap.At, Type: FileModified, Path: path, Meta: &cur})
			}
		}
		// deleted
		for path, prev := range w.curr.Files {
			if _, ok := snap.Files[path]; !ok {
				p := prev
				snap.Events = append(snap.Events, FileEvent{At: snap.At, Type: FileDeleted, Path: path, Meta: &p})
			}
		}
		sort.Slice(snap.Events, func(i, j int) bool {
			if snap.Events[i].Type == snap.Events[j].Type {
				return snap.Events[i].Path < snap.Events[j].Path
			}
			return snap.Events[i].Type < snap.Events[j].Type
		})
	}

	w.prev = w.curr
	w.curr = snap
	return nil
}

func (w *FileWatchCapturer) GetInfo() (string, error) {
	if w.curr == nil {
		return "FileWatchSnapshot(empty)", nil
	}
	// short sample
	sample := ""
	if len(w.curr.Events) > 0 {
		e := w.curr.Events[0]
		sample = fmt.Sprintf(", sample=%s:%s", e.Type, filepath.Base(e.Path))
		if len(w.curr.Events) > 1 {
			sample += fmt.Sprintf("(+%d)", len(w.curr.Events)-1)
		}
	}
	return fmt.Sprintf(
		"FileWatchSnapshot(at=%s, files=%d, events=%d%s)",
		w.curr.At.Format(time.RFC3339),
		len(w.curr.Files),
		len(w.curr.Events),
		sample,
	), nil
}

func defaultWatchPaths() []string {
	var paths []string

	home, _ := os.UserHomeDir()
	paths = append(paths,
		filepath.Join(home, "Downloads"),
		filepath.Join(home, ".config", "autostart"),
	)
	uniq := make(map[string]struct{})
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, ok := uniq[p]; ok {
			continue
		}
		uniq[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
