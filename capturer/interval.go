package capturer

import "time"

// DefaultIntervalFor returns the suggested interval for a capturer.
func DefaultIntervalFor(c Capturer) time.Duration {
	switch c.(type) {
	case *CPUCapturer:
		return 1 * time.Second
	case *NETCapturer, *ConnCapturer:
		return 5 * time.Second
	case *ProcCapturer, *MEMCapturer:
		return 5 * time.Second
	case *FileWatchCapturer:
		return 15 * time.Second
	case *DISKCapturer:
		return 30 * time.Second
	case *PersistCapturer:
		return 10 * time.Minute
	default:
		return 5 * time.Second
	}
}
