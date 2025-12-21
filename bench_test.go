package miniedr

import (
	"io"
	"testing"

	"github.com/jaewooli/miniedr/capturer"
)

func BenchmarkDetectorEvaluate(b *testing.B) {
	det := &Detector{Rules: DefaultRules()}
	info := capturer.InfoData{
		Metrics: map[string]float64{
			"cpu.total_pct":          95,
			"mem.ram.used_pct":       92,
			"mem.swap.used_pct":      70,
			"proc.new":               12,
			"net.rx_bytes_per_sec":   800 * 1024,
			"net.tx_bytes_per_sec":   800 * 1024,
			"file.events":            60,
			"persist.added":          1,
			"persist.changed":        0,
			"persist.removed":        0,
			"persist.total_changes":  1,
		},
		Summary: "Persist snapshot change",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = det.Evaluate(info)
	}
}

func BenchmarkAlertPipelineProcess(b *testing.B) {
	det := &Detector{Rules: DefaultRules()}
	resp := &ResponderPipeline{Responders: []AlertResponder{&LogResponder{Out: io.Discard}}}
	pipeline := &AlertPipeline{Detector: det, Responder: resp}
	info := capturer.InfoData{
		Metrics: map[string]float64{
			"cpu.total_pct":         95,
			"mem.ram.used_pct":      92,
			"mem.swap.used_pct":     70,
			"proc.new":              12,
			"net.rx_bytes_per_sec":  800 * 1024,
			"net.tx_bytes_per_sec":  800 * 1024,
			"file.events":           60,
			"persist.added":         1,
			"persist.changed":       0,
			"persist.removed":       0,
			"persist.total_changes": 1,
		},
		Summary: "Persist snapshot change",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pipeline.Process(info)
	}
}
