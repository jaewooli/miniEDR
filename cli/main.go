package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jaewooli/miniedr"
	"github.com/jaewooli/miniedr/agent"
	"github.com/jaewooli/miniedr/capturer"
	dash "github.com/jaewooli/miniedr/dashboard"
)

func main() {
	verbose := flag.Bool("verbose", false, "print detailed capture output")
	dashboardEnabled := flag.Bool("dashboard", true, "run dashboard server; set false to run agent-only")
	dashboardAddr := flag.String("dashboard-addr", ":8090", "dashboard listen addr")
	dashboardTitle := flag.String("dashboard-title", "miniEDR Dashboard", "dashboard page title")
	dashboardAuto := flag.Bool("dashboard-autorefresh", false, "enable dashboard auto-refresh")
	dashboardAutoSec := flag.Int("dashboard-refresh-sec", 10, "dashboard auto-refresh interval seconds (default 10)")
	dashboardEventRefresh := flag.Bool("dashboard-event-refresh", true, "refresh dashboard when captures complete (enables per-capturer intervals)")
	dashboardCaptureSec := flag.Int("dashboard-capture-sec", 0, "dashboard capture interval seconds (0 uses per-capturer defaults)")
	telemetryPath := flag.String("telemetry-file", "", "path to write telemetry JSON lines (rotates at ~5MB)")
	configPath := flag.String("config", "", "path to config file (default: auto-detect config.yaml)")
	flag.Parse()

	printStartupHelp(*dashboardEnabled, *dashboardAddr)

	cb := capturer.NewCapturersBuilder()
	if path := resolveConfigPath(*configPath); path != "" {
		cb.SetConfigFile(path)
	}

	capturers, err := cb.Build()

	if err != nil {
		log.Fatalf("there is an error: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *dashboardEnabled {
		ds := dash.NewDashboardServer(capturers, *dashboardTitle, *verbose)
		ds.SetAutoRefresh(*dashboardAuto, *dashboardAutoSec)
		ds.SetEventRefresh(*dashboardEventRefresh)
		if *dashboardCaptureSec >= 0 {
			ds.SetCaptureInterval(time.Duration(*dashboardCaptureSec) * time.Second)
		}
		log.Printf("dashboard listening on %s", *dashboardAddr)
		if err := ds.Run(ctx, *dashboardAddr); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatalf("dashboard error: %v", err)
		}
		return
	}

	schedules := miniedr.DefaultSchedules(capturers)
	edrAgent := agent.NewCollectAgent(schedules)
	edrAgent.Out = os.Stdout
	edrAgent.Verbose = *verbose
	if *telemetryPath != "" {
		edrAgent.AddSink(miniedr.NewJSONFileSink(*telemetryPath, 0))
	}

	if err := edrAgent.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("agent error: %v", err)
	}
}

func printStartupHelp(dashboard bool, addr string) {
	url := addr
	if strings.HasPrefix(addr, ":") {
		url = "localhost" + addr
	}
	url = "http://" + url

	if dashboard {
		log.Printf("dashboard enabled at %s (disable with -dashboard=false)", url)
	} else {
		log.Printf("dashboard disabled (enable with -dashboard=true, default addr %s)", url)
	}
	log.Printf("run with -h for full options")
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
