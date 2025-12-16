package main

import (
	"bytes"
	"fmt"
	"github.com/jaewooli/miniedr"
	"log"
)

func main() {
	io := &bytes.Buffer{}
	cb := miniedr.NewCapturersBuilder()

	capturers, err := cb.Build()

	if err != nil {
		log.Fatalf("ther is an error: %v", err)
	}

	sm := miniedr.NewSnapshotManager(io, capturers)

	if err := sm.Capture(); err != nil {
		log.Fatalf("ther is an error: %v", err)
	}
	fmt.Println(sm.GetInfo())
}
