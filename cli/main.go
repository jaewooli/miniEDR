package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/jaewooli/miniedr"
)

func main() {
	verbose := flag.Bool("verbose", false, "print detailed capture output")
	configPath := flag.String("config", "", "path to config file (default: auto-detect config.yaml)")
	flag.Parse()

	cb := miniedr.NewCapturersBuilder()
	if path := resolveConfigPath(*configPath); path != "" {
		cb.SetConfigFile(path)
	}

	capturers, err := cb.Build()

	if err != nil {
		log.Fatalf("there is an error: %v", err)
	}

	schedules := miniedr.DefaultSchedules(capturers)
	agent := miniedr.NewEDRAgent(schedules)
	agent.Out = os.Stdout
	agent.Verbose = *verbose

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := agent.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("agent error: %v", err)
	}
}

func resolveConfigPath(userPath string) string {
	try := func(p string) (string, bool) {
		if p == "" {
			return "", false
		}
		info, err := os.Stat(p)
		if err == nil && !info.IsDir() {
			return p, true
		}
		return "", false
	}

	if p, ok := try(userPath); ok {
		return p
	}
	if p, ok := try("config.yaml"); ok {
		return p
	}
	exe, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exe)
		if p, ok := try(filepath.Join(exeDir, "config.yaml")); ok {
			return p
		}
		if p, ok := try(filepath.Join(exeDir, "..", "config.yaml")); ok {
			return p
		}
	}
	return ""
}
