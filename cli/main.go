package main

import (
	"context"
	"errors"
	"github.com/jaewooli/miniedr"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	cb := miniedr.NewCapturersBuilder()
	cb.SetConfigFile("../config.yaml")

	capturers, err := cb.Build()

	if err != nil {
		log.Fatalf("ther is an error: %v", err)
	}

	sm := miniedr.NewSnapshotManager(os.Stdout, capturers)
	agent := miniedr.NewEDRAgent(sm)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := agent.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("agent error: %v", err)
	}
}
