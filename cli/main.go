package main

import (
	"bytes"
	"fmt"
	"github.com/jaewooli/miniedr"
)

func main() {
	io := &bytes.Buffer{}
	SM := miniedr.NewSnapshotManager(io)

	SM.Capture()
	fmt.Print(SM.GetInfo())
}
