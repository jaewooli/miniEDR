package capturer

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func benchCapturer(b *testing.B, c Capturer) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := c.Capture(); err != nil {
			b.Fatalf("capture: %v", err)
		}
		if _, err := c.GetInfo(); err != nil {
			b.Fatalf("getinfo: %v", err)
		}
	}
}

func BenchmarkCPUCapturer(b *testing.B) {
	benchCapturer(b, NewCPUCapturer())
}

func BenchmarkMEMCapturer(b *testing.B) {
	benchCapturer(b, NewMEMCapturer())
}

func BenchmarkNETCapturer(b *testing.B) {
	benchCapturer(b, NewNETCapturer())
}

func BenchmarkDISKCapturer(b *testing.B) {
	benchCapturer(b, NewDISKCapturer("/"))
}

func BenchmarkProcCapturer(b *testing.B) {
	benchCapturer(b, NewProcCapturer())
}

func BenchmarkPersistCapturer(b *testing.B) {
	benchCapturer(b, NewPersistCapturer())
}

func BenchmarkConnCapturer(b *testing.B) {
	benchCapturer(b, NewConnCapturer("all"))
}

func BenchmarkFileChangeCapturer(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 5; i++ {
		path := filepath.Join(dir, "benchfile"+strconv.Itoa(i))
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			b.Fatalf("write file: %v", err)
		}
	}
	fc := NewFileChangeCapturer(dir)
	fc.Paths = []string{dir}
	fc.MaxFiles = 1000
	benchCapturer(b, fc)
}
