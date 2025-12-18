package miniedr

import (
	"time"

	"github.com/jaewooli/miniedr/capturer"
)

// CapturerSchedule binds a capturer to a capture interval.
type CapturerSchedule struct {
	Capturer capturer.Capturer
	Interval time.Duration
}

// DefaultSchedules assigns reasonable intervals per capturer type.
func DefaultSchedules(cs []capturer.Capturer) []CapturerSchedule {
	var out []CapturerSchedule
	for _, c := range cs {
		out = append(out, CapturerSchedule{
			Capturer: c,
			Interval: capturer.DefaultIntervalFor(c),
		})
	}
	return out
}
