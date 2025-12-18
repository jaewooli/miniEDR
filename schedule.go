package miniedr

import "time"

// CapturerSchedule binds a capturer to a capture interval.
type CapturerSchedule struct {
	Capturer Capturer
	Interval time.Duration
}

// DefaultSchedules assigns reasonable intervals per capturer type.
func DefaultSchedules(cs []Capturer) []CapturerSchedule {
	var out []CapturerSchedule
	for _, c := range cs {
		out = append(out, CapturerSchedule{
			Capturer: c,
			Interval: DefaultIntervalFor(c),
		})
	}
	return out
}

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
