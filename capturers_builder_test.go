package miniedr_test

import (
	"testing"

	"github.com/jaewooli/miniedr"
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
	gotBuilder := miniedr.NewCapturersBuilder()
	got, err := gotBuilder.Build()

	assertError(t, err, "")

	want := []miniedr.Capturer{
		miniedr.NewCPUCapturer(),
		miniedr.NewConnCapturer("all"),
		miniedr.NewDISKCapturer(),
		miniedr.NewFileWatchCapturer(),
		miniedr.NewMEMCapturer(),
		miniedr.NewNETCapturer(),
		miniedr.NewPersistCapturer(),
		miniedr.NewProcCapturer(),
	}

	assertCapturers(t, got, want)

	t.Run("setting config", func(t *testing.T) {
		gotBuilder := miniedr.NewCapturersBuilder()
		gotBuilder.SetConfig(exampleconfig)

		got, err := gotBuilder.Build()

		assertError(t, err, "")

		want = miniedr.Capturers{
			miniedr.NewCPUCapturer(),
			miniedr.NewConnCapturer("all"),
			miniedr.NewDISKCapturer(),
			miniedr.NewFileWatchCapturer(),
			miniedr.NewMEMCapturer(),
			miniedr.NewNETCapturer(),
			miniedr.NewPersistCapturer(),
			miniedr.NewProcCapturer(),
		}
		assertCapturers(t, got, want)
	})
}
