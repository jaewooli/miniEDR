package capturer_test

import (
	"testing"

	"github.com/jaewooli/miniedr/capturer"
)

const exampleconfig = `capturers:
  cpu:
    enabled: true

  conn:
    enabled: true
    kind: all

  disk:
    enabled: true
    paths: ["/"]       

  filewatch:
    enabled: true
    paths: ["/tmp", "/var/tmp"]
    max_files: 50000

  mem:
    enabled: true

  net:
    enabled: true

  persist:
    enabled: true 

  proc:
    enabled: true
`

func TestCapturersBuilder(t *testing.T) {
	gotBuilder := capturer.NewCapturersBuilder()
	got, err := gotBuilder.Build()

	assertError(t, err, "")

	want := []capturer.Capturer{
		capturer.NewCPUCapturer(),
		capturer.NewConnCapturer("all"),
		capturer.NewDISKCapturer(),
		capturer.NewFileWatchCapturer(),
		capturer.NewMEMCapturer(),
		capturer.NewNETCapturer(),
		capturer.NewPersistCapturer(),
		capturer.NewProcCapturer(),
	}

	assertCapturers(t, got, want)

	t.Run("setting config", func(t *testing.T) {
		gotBuilder := capturer.NewCapturersBuilder()
		gotBuilder.SetConfig(exampleconfig)

		got, err := gotBuilder.Build()

		assertError(t, err, "")

		want = capturer.Capturers{
			capturer.NewCPUCapturer(),
			capturer.NewConnCapturer("all"),
			capturer.NewDISKCapturer(),
			capturer.NewFileWatchCapturer(),
			capturer.NewMEMCapturer(),
			capturer.NewNETCapturer(),
			capturer.NewPersistCapturer(),
			capturer.NewProcCapturer(),
		}
		assertCapturers(t, got, want)
	})
}
