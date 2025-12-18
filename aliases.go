package miniedr

import (
	"io"
	"time"

	"github.com/jaewooli/miniedr/capturer"
)

// Re-export capturer types/functions for backwards compatibility.
type (
	Capturer          = capturer.Capturer
	Capturers         = capturer.Capturers
	VerboseInfo       = capturer.VerboseInfo
	Info              = capturer.Info
	InfoData          = capturer.InfoData
	PersistSource     = capturer.PersistSource
	SnapshotManager   = capturer.SnapshotManager
	CPUCapturer       = capturer.CPUCapturer
	MEMCapturer       = capturer.MEMCapturer
	DISKCapturer      = capturer.DISKCapturer
	NETCapturer       = capturer.NETCapturer
	ConnCapturer      = capturer.ConnCapturer
	FileWatchCapturer = capturer.FileWatchCapturer
	PersistCapturer   = capturer.PersistCapturer
	ProcCapturer      = capturer.ProcCapturer
	FileMeta          = capturer.FileMeta
	FileEvent         = capturer.FileEvent
	FileEventType     = capturer.FileEventType
)

// Constructors passthrough
func NewCPUCapturer() *capturer.CPUCapturer { return capturer.NewCPUCapturer() }
func NewConnCapturer(kind string) *capturer.ConnCapturer {
	return capturer.NewConnCapturer(kind)
}
func NewDISKCapturer(paths ...string) *capturer.DISKCapturer {
	return capturer.NewDISKCapturer(paths...)
}
func NewFileWatchCapturer(paths ...string) *capturer.FileWatchCapturer {
	return capturer.NewFileWatchCapturer(paths...)
}
func NewMEMCapturer() *capturer.MEMCapturer         { return capturer.NewMEMCapturer() }
func NewNETCapturer() *capturer.NETCapturer         { return capturer.NewNETCapturer() }
func NewPersistCapturer() *capturer.PersistCapturer { return capturer.NewPersistCapturer() }
func NewProcCapturer() *capturer.ProcCapturer       { return capturer.NewProcCapturer() }
func NewSnapshotManager(out io.Writer, cs []Capturer) *capturer.SnapshotManager {
	return capturer.NewSnapshotManager(out, cs)
}

// Builders and helpers
func NewCapturersBuilder() *capturer.CapturersBuilder { return capturer.NewCapturersBuilder() }
func CapturerName(c Capturer) string                  { return capturer.CapturerName(c) }
func DefaultIntervalFor(c Capturer) time.Duration     { return capturer.DefaultIntervalFor(c) }
