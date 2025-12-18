package miniedr

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jaewooli/miniedr/capturer"
)

func TestJSONFileSinkWritesAndRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.json")
	sink := NewJSONFileSink(path, 200) // small to force rotation

	info := capturer.InfoData{
		Summary: "test",
		Meta: capturer.TelemetryMeta{
			Host:       "h",
			Session:    "s",
			Timezone:   "+0000",
			CapturedAt: time.Unix(0, 0),
		},
		Metrics: map[string]float64{"a": 1},
	}

	// write a few entries to trigger rotation
	for i := 0; i < 5; i++ {
		if err := sink.Consume(info); err != nil {
			t.Fatalf("consume %d: %v", i, err)
		}
	}

	files, err := filepath.Glob(filepath.Join(dir, "telemetry.json*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) < 2 {
		t.Fatalf("expected rotation to create multiple files, got %v", files)
	}

	// ensure new file exists
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected sink file to exist after rotation: %v", err)
	}
}
