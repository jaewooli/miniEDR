package dashboard

import (
	"context"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jaewooli/miniedr/capturer"
)

// DashboardServer exposes a lightweight HTML dashboard to view capturer snapshots.
// It maintains a background capture loop and can push refresh events to the UI.
type DashboardServer struct {
	Capturers capturer.Capturers

	mu              sync.RWMutex
	tmpl            *template.Template
	nowFn           func() time.Time
	title           string
	verbose         bool
	autoRefresh     bool
	eventRefresh    bool
	refreshSeconds  int
	captureInterval time.Duration
	displayInterval time.Duration

	snapshot      dashboardData
	hasSnapshot   bool
	clients       map[chan string]struct{}
	lastPayload   map[string]string
	logs          map[string][]dashboardLogEntry
	items         map[string]dashboardItem
	itemIntervals map[string]time.Duration
	netScales     map[string]float64
	countScales   map[string]map[string]float64 // item -> label -> scale
}

type dashboardItem struct {
	Name    string
	Info    capturer.InfoData
	Verbose string
	Error   string
	Changed bool
	Warming bool
	Logs    []dashboardLogEntry
	Graphs  []graphInfo
	Display string
	Meta    capturer.TelemetryMeta
}

type dashboardLogEntry struct {
	At      string
	Info    string
	Verbose string
	Error   string
	Changed bool
	Warming bool
}

type graphInfo struct {
	Label     string
	Value     float64
	Display   float64
	ValueText string
	MaxHint   string // optional hint about the scale (e.g., "scale=10MB/s")
}

type dashboardData struct {
	Title             string
	RefreshedAt       string
	Items             []dashboardItem
	AutoRefresh       bool
	RefreshSecs       int
	EventRefresh      bool
	CaptureIntervalMS int64
}

func NewDashboardServer(capturers capturer.Capturers, title string, verbose bool) *DashboardServer {
	t := template.Must(template.New("dashboard").Funcs(template.FuncMap{
		"divMS":  divMS,
		"chartX": chartX,
		"hasNL":  func(s string) bool { return strings.Contains(s, "\n") },
	}).Parse(dashboardHTML))
	if title == "" {
		title = "miniEDR Dashboard"
	}
	displayInterval := minCapturerInterval(capturers)
	return &DashboardServer{
		Capturers:       capturers,
		tmpl:            t,
		nowFn:           time.Now,
		title:           title,
		verbose:         verbose,
		refreshSeconds:  10,
		eventRefresh:    true,
		captureInterval: 0, // per-capturer intervals by default
		displayInterval: displayInterval,
		clients:         make(map[chan string]struct{}),
		lastPayload:     make(map[string]string),
		logs:            make(map[string][]dashboardLogEntry),
	}
}

// SetNowFunc overrides the clock for testing.
func (d *DashboardServer) SetNowFunc(fn func() time.Time) {
	if fn == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.nowFn = fn
}

// SetAutoRefresh configures default auto-refresh behavior and interval in seconds.
func (d *DashboardServer) SetAutoRefresh(enabled bool, seconds int) {
	if seconds <= 0 {
		seconds = 5
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.autoRefresh = enabled
	d.refreshSeconds = seconds
}

// SetEventRefresh toggles refresh-on-capture behavior exposed to the UI.
func (d *DashboardServer) SetEventRefresh(enabled bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.eventRefresh = enabled
}

// SetCaptureInterval updates the background capture interval.
func (d *DashboardServer) SetCaptureInterval(interval time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if interval < 0 {
		return
	}
	if interval == 0 {
		d.captureInterval = 0
		d.displayInterval = minCapturerInterval(d.Capturers)
		return
	}
	d.captureInterval = interval
	d.displayInterval = interval
}

// CaptureNow forces an immediate capture (useful for tests).
func (d *DashboardServer) CaptureNow() {
	d.captureAndStore()
}

// Run starts the HTTP server and blocks until ctx is done or the server stops.
func (d *DashboardServer) Run(ctx context.Context, addr string) error {
	d.ensureInitialSnapshot()
	go d.captureLoop(ctx)

	mux := http.NewServeMux()
	mux.Handle("/", d)
	mux.HandleFunc("/events", d.serveEvents)

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Graceful shutdown
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (d *DashboardServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	d.ensureInitialSnapshot()
	snap := d.currentSnapshot()
	if err := d.tmpl.Execute(w, snap); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ServeHTTP implements http.Handler.
func (d *DashboardServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.handleDashboard(w, r)
}

func (d *DashboardServer) captureAndStore() {
	d.mu.RLock()
	nowFn := d.nowFn
	title := d.title
	verbose := d.verbose
	autoRefresh := d.autoRefresh
	refreshSecs := d.refreshSeconds
	eventRefresh := d.eventRefresh
	capInterval := d.displayInterval
	d.mu.RUnlock()

	for _, c := range d.Capturers {
		d.captureSingle(c, nowFn().Format(time.RFC3339), title, verbose, autoRefresh, refreshSecs, eventRefresh, capInterval)
	}
}

func (d *DashboardServer) captureSingle(c capturer.Capturer, ref, title string, verbose bool, autoRefresh bool, refreshSecs int, eventRefresh bool, capInterval time.Duration) {
	name := capturer.CapturerName(c)

	item := dashboardItem{Name: name}

	if err := c.Capture(); err != nil {
		item.Error = fmt.Sprintf("capture error: %v", err)
	} else {
		info, err := c.GetInfo()
		if err != nil {
			item.Error = fmt.Sprintf("getinfo error: %v", err)
		} else {
			if info.Meta.Host == "" {
				info.Meta.Host, _ = os.Hostname()
			}
			if info.Meta.AgentVersion == "" {
				info.Meta.AgentVersion = "dev"
			}
			if info.Meta.CapturedAt.IsZero() {
				info.Meta.CapturedAt = time.Now()
			}
			item.Info = info
			item.Meta = info.Meta
			if verbose {
				if vc, ok := c.(capturer.VerboseInfo); ok {
					verb, err := vc.GetVerboseInfo()
					if err != nil {
						item.Error = fmt.Sprintf("getverboseinfo error: %v", err)
					} else {
						item.Verbose = verb
					}
				}
			}
			netScale := d.updateNetScale(name, item.Info)
			countScale := func(label string, val float64) float64 { return d.updateCountScale(name, label, val) }
			item.Graphs = append(item.Graphs, deriveGraphs(name, item.Info, netScale, countScale)...)
			item.Display = summarizeInfo(name, item.Info)
		}
	}

	payload := normalizePayload(item.Info.Summary) + "|" + metricsFingerprint(item.Info.Metrics) + "|" + normalizePayload(item.Verbose) + "|" + normalizePayload(item.Error)
	d.mu.RLock()
	prev := d.lastPayload[name]
	d.mu.RUnlock()
	if prev != "" && prev != payload {
		item.Changed = true
	}
	if w, ok := c.(interface{ IsWarm() bool }); ok && !w.IsWarm() {
		item.Warming = true
	}
	if item.Warming {
		if item.Display != "" {
			item.Display += " · "
		}
		item.Display += "warming up"
	}

	entry := dashboardLogEntry{
		At:      ref,
		Info:    item.Info.Summary,
		Verbose: item.Verbose,
		Error:   item.Error,
		Changed: item.Changed,
		Warming: item.Warming,
	}

	d.mu.Lock()
	logs := append([]dashboardLogEntry{}, d.logs[name]...)
	logs = append(logs, entry)
	if len(logs) > 50 {
		logs = logs[len(logs)-50:]
	}
	item.Logs = logs

	d.lastPayload[name] = payload
	d.logs[name] = logs

	// store latest item
	if d.items == nil {
		d.items = make(map[string]dashboardItem)
	}
	if d.itemIntervals == nil {
		d.itemIntervals = make(map[string]time.Duration)
	}
	d.items[name] = item
	d.itemIntervals[name] = capturer.DefaultIntervalFor(c)

	// rebuild items slice
	items := make([]dashboardItem, 0, len(d.items))
	for _, it := range d.items {
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		ii := d.itemIntervals[items[i].Name]
		ij := d.itemIntervals[items[j].Name]
		if ii != ij {
			return ii < ij
		}
		return items[i].Name < items[j].Name
	})

	d.snapshot = dashboardData{
		Title:             title,
		RefreshedAt:       ref,
		Items:             items,
		AutoRefresh:       autoRefresh,
		RefreshSecs:       refreshSecs,
		EventRefresh:      eventRefresh,
		CaptureIntervalMS: capInterval.Milliseconds(),
	}
	d.hasSnapshot = true
	d.mu.Unlock()

	d.broadcast(ref)
}

func (d *DashboardServer) currentSnapshot() dashboardData {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.snapshot
}

func (d *DashboardServer) captureLoop(ctx context.Context) {
	d.mu.RLock()
	interval := d.captureInterval
	capturers := append(capturer.Capturers{}, d.Capturers...)
	d.mu.RUnlock()

	// per-capturer intervals when interval == 0
	if interval <= 0 {
		var wg sync.WaitGroup
		for _, c := range capturers {
			wg.Add(1)
			go func(c capturer.Capturer) {
				defer wg.Done()
				d.runPerCapturer(ctx, c)
			}(c)
		}
		wg.Wait()
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		d.captureAndStore()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			continue
		}
	}
}

func (d *DashboardServer) ensureInitialSnapshot() {
	d.mu.RLock()
	has := d.hasSnapshot
	d.mu.RUnlock()
	if has {
		return
	}
	d.captureAndStore()
}

func (d *DashboardServer) runPerCapturer(ctx context.Context, c capturer.Capturer) {
	interval := capturer.DefaultIntervalFor(c)
	if interval <= 0 {
		interval = 5 * time.Second
	}

	d.mu.RLock()
	nowFn := d.nowFn
	title := d.title
	verbose := d.verbose
	autoRefresh := d.autoRefresh
	refreshSecs := d.refreshSeconds
	eventRefresh := d.eventRefresh
	capInterval := d.displayInterval
	hasSnap := d.hasSnapshot
	name := capturer.CapturerName(c)
	_, hasItem := d.items[name]
	d.mu.RUnlock()

	if !hasSnap || !hasItem {
		ref := nowFn().Format(time.RFC3339)
		d.captureSingle(c, ref, title, verbose, autoRefresh, refreshSecs, eventRefresh, capInterval)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ref := nowFn().Format(time.RFC3339)
			d.captureSingle(c, ref, title, verbose, autoRefresh, refreshSecs, eventRefresh, capInterval)
		}
	}
}

func (d *DashboardServer) broadcast(msg string) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for ch := range d.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (d *DashboardServer) serveEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan string, 1)
	d.mu.Lock()
	d.clients[ch] = struct{}{}
	d.mu.Unlock()

	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		// Send comment to flush headers and keep connection open.
		fmt.Fprintf(w, ":ok\n\n")
		flusher.Flush()
	}
	defer func() {
		d.mu.Lock()
		delete(d.clients, ch)
		d.mu.Unlock()
		close(ch)
	}()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// ServeEvents exposes the SSE handler (useful for tests).
func (d *DashboardServer) ServeEvents(w http.ResponseWriter, r *http.Request) {
	d.serveEvents(w, r)
}

const dashboardHTML = `
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<title>{{.Title}}</title>
<style>
body {
  margin: 0;
  font-family: "Fira Sans", "Segoe UI", "Trebuchet MS", system-ui, -apple-system, sans-serif;
  background: radial-gradient(circle at 20% 20%, #0f172a, #0b1220 35%, #050915);
  color: #e5e7eb;
  min-height: 100vh;
}
.shell {
  max-width: 1200px;
  margin: 0 auto;
  padding: 32px 20px 80px;
}
.topbar {
  display: flex;
  align-items: baseline;
  gap: 16px;
  flex-wrap: wrap;
}
.title {
  font-size: 28px;
  font-weight: 700;
  letter-spacing: 0.4px;
}
.meta {
  color: #94a3b8;
  font-size: 14px;
}
.actions {
  margin-left: auto;
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
}
.control {
  display: flex;
  align-items: center;
  gap: 8px;
  color: #cbd5e1;
  font-size: 13px;
}
.control input[type="number"] {
  width: 70px;
  padding: 6px 8px;
  border-radius: 8px;
  border: 1px solid rgba(148, 163, 184, 0.5);
  background: rgba(15, 23, 42, 0.6);
  color: #e5e7eb;
}
.toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  user-select: none;
}
input[type="checkbox"] {
  width: 16px;
  height: 16px;
}
button {
  background: linear-gradient(120deg, #22d3ee, #3b82f6);
  color: #0b1220;
  border: none;
  border-radius: 10px;
  padding: 10px 14px;
  font-weight: 700;
  cursor: pointer;
  box-shadow: 0 8px 30px rgba(34, 211, 238, 0.25);
  transition: transform 120ms ease, box-shadow 120ms ease;
}
button:hover {
  transform: translateY(-1px);
  box-shadow: 0 10px 34px rgba(59, 130, 246, 0.3);
}
.grid {
  margin-top: 24px;
  display: grid;
  gap: 20px;
  grid-template-columns: repeat(auto-fit, minmax(360px, 1fr));
  align-items: stretch;
}
.card {
  background: linear-gradient(135deg, rgba(148, 163, 184, 0.08), rgba(100, 116, 139, 0.05));
  border: 1px solid rgba(148, 163, 184, 0.2);
  border-radius: 14px;
  padding: 16px;
  box-shadow: 0 10px 40px rgba(0,0,0,0.3);
  backdrop-filter: blur(4px);
  display: flex;
  flex-direction: column;
  gap: 10px;
  height: 100%;
  box-sizing: border-box;
  min-width: 0;
}
.card h2 {
  margin: 0;
  font-size: 18px;
  letter-spacing: 0.3px;
}
.changed {
  background: linear-gradient(120deg, #22c55e, #10b981);
  color: #0b1220;
}
.pill {
  display: inline-block;
  padding: 4px 10px;
  border-radius: 999px;
  font-size: 12px;
  letter-spacing: 0.2px;
  background: #0ea5e9;
  color: #0b1220;
  font-weight: 700;
}
.pill.warm {
  background: #fbbf24;
  color: #0b1220;
}
.error {
  color: #fca5a5;
  font-weight: 600;
}
.chart {
  margin-top: 6px;
}
 .gauge {
  width: 150px;
  height: 150px;
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.gauge-ring {
  position: absolute;
  inset: 0;
  border-radius: 50%;
  background: conic-gradient(
    #22d3ee calc(var(--val) * 1%),
    rgba(148,163,184,0.2) calc(var(--val) * 1%),
    rgba(148,163,184,0.2) 100%
  );
}
.gauge-center {
  position: relative;
  width: 100px;
  height: 100px;
  border-radius: 50%;
  background: #0b1220;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4px;
  color: #e5e7eb;
  box-shadow: inset 0 0 0 1px rgba(148,163,184,0.3);
  font-weight: 700;
  text-align: center;
}
.gauge-label {
  font-size: 13px;
  color: #94a3b8;
  font-weight: 600;
  max-width: 110px;
  line-height: 1.2;
  word-break: break-word;
}
.gauge-hint {
  font-size: 11px;
  color: #64748b;
}
.gauge-label.gauge-label-small {
  font-size: 12px;
}
.gauge-grid {
  display: grid;
  justify-content: center;
  justify-items: center;
  gap: 12px;
}
.gauge-grid-1 {
  grid-template-columns: 1fr;
}
.gauge-grid-2col {
  grid-template-columns: 1fr;
}
.gauge-grid-row {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  justify-content: center;
  align-items: center;
}
.gauge-lg {
  width: 230px;
  height: 230px;
}
.gauge-lg .gauge-center {
  width: 150px;
  height: 150px;
}
.gauge-md {
  width: 180px;
  height: 180px;
}
.gauge-md .gauge-center {
  width: 120px;
  height: 120px;
}
.gauge {
  justify-self: center;
}
svg.timeline line {
  stroke: rgba(148, 163, 184, 0.6);
  stroke-width: 2;
}
.timeline {
  width: 100%;
}
svg.timeline circle {
  fill: #22d3ee;
  stroke: #0b1220;
  stroke-width: 1.5;
}
svg.timeline circle.changed {
  fill: #22c55e;
}
svg.timeline circle.error {
  fill: #f87171;
}
details summary {
  cursor: pointer;
  color: #cbd5e1;
  font-weight: 600;
}
details {
  background: rgba(15, 23, 42, 0.5);
  border: 1px solid rgba(148, 163, 184, 0.2);
  border-radius: 10px;
  padding: 8px 10px;
}
details pre {
  margin-top: 6px;
}
pre {
  background: rgba(15, 23, 42, 0.7);
  border: 1px solid rgba(148, 163, 184, 0.2);
  border-radius: 10px;
  padding: 12px;
  margin: 0;
  color: #cbd5e1;
  white-space: pre-wrap;
  word-break: break-word;
  overflow-wrap: anywhere;
  font-family: "JetBrains Mono", "SFMono-Regular", Consolas, Menlo, monospace;
  font-size: 13px;
}
.stack {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
small {
  color: #94a3b8;
}
.hint {
  color: #94a3b8;
  font-size: 12px;
}
.summary {
  color: #e5e7eb;
  font-weight: 600;
  margin-top: 4px;
  white-space: pre-wrap;
  word-break: break-all;
  overflow-wrap: anywhere;
}
.detail-box {
  max-height: 260px;
  overflow: auto;
  padding-right: 4px;
}
</style>
</head>
<body>
  <div class="shell">
    <div class="topbar">
      <div>
        <div class="title">{{.Title}}</div>
        <div class="meta">Refreshed at {{.RefreshedAt}} • Capture every {{printf "%.0f" (divMS .CaptureIntervalMS) }}s</div>
      </div>
      <div class="actions">
        <div class="control toggle">
          <input type="checkbox" id="eventRefresh" {{if .EventRefresh}}checked{{end}} />
          <label for="eventRefresh">Refresh on capture</label>
        </div>
        <div class="control toggle">
          <input type="checkbox" id="autoRefresh" {{if .AutoRefresh}}checked{{end}} />
          <label for="autoRefresh">Timed refresh</label>
        </div>
        <div class="control">
          every <input type="number" id="refreshInterval" min="1" value="{{.RefreshSecs}}" /> sec
        </div>
        <button id="refreshBtn" onclick="location.reload()">Refresh now</button>
      </div>
    </div>

    <div class="grid">
      {{range .Items}}
      <div class="card">
        <div class="stack">
          <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;">
            <h2>{{.Name}}</h2>
            {{if .Error}}<span class="pill" style="background:#f87171;color:#0b1220;">error</span>{{end}}
            {{if .Changed}}<span class="pill changed">changed</span>{{end}}
            {{if .Warming}}<span class="pill warm">warming up</span>{{end}}
          </div>
          {{if .Error}}
            <div class="error">{{.Error}}</div>
          {{else}}
            {{if .Graphs}}
            {{/* Choose layout based on graph count: 1 -> enlarged; 2 -> column; else -> row wrap */}}
            {{if eq (len .Graphs) 1}}
              <div class="gauge-grid gauge-grid-1">
                {{range .Graphs}}
                <div class="gauge gauge-lg" style="--val: {{printf "%.1f" .Display}}">
                  <div class="gauge-ring"></div>
                  <div class="gauge-center">
                    <div>{{if .ValueText}}{{.ValueText}}{{else}}{{printf "%.1f%%" .Value}}{{end}}</div>
                    <div class="gauge-label {{if hasNL .Label}}gauge-label-small{{end}}">{{.Label}}</div>
                    {{if .MaxHint}}<div class="gauge-hint">{{.MaxHint}}</div>{{end}}
                  </div>
                </div>
                {{end}}
              </div>
            {{else if eq (len .Graphs) 2}}
              <div class="gauge-grid gauge-grid-2col">
                {{range .Graphs}}
                <div class="gauge gauge-md" style="--val: {{printf "%.1f" .Display}}">
                  <div class="gauge-ring"></div>
                  <div class="gauge-center">
                    <div>{{if .ValueText}}{{.ValueText}}{{else}}{{printf "%.1f%%" .Value}}{{end}}</div>
                    <div class="gauge-label {{if hasNL .Label}}gauge-label-small{{end}}">{{.Label}}</div>
                    {{if .MaxHint}}<div class="gauge-hint">{{.MaxHint}}</div>{{end}}
                  </div>
                </div>
                {{end}}
              </div>
            {{else}}
              <div class="gauge-grid gauge-grid-row">
                {{range .Graphs}}
                <div class="gauge" style="--val: {{printf "%.1f" .Display}}">
                  <div class="gauge-ring"></div>
                  <div class="gauge-center">
                    <div>{{if .ValueText}}{{.ValueText}}{{else}}{{printf "%.1f%%" .Value}}{{end}}</div>
                    <div class="gauge-label {{if hasNL .Label}}gauge-label-small{{end}}">{{.Label}}</div>
                    {{if .MaxHint}}<div class="gauge-hint">{{.MaxHint}}</div>{{end}}
                  </div>
                </div>
                {{end}}
              </div>
            {{end}}
            {{end}}
            <div>
              <small>summary</small>
              <div class="summary">{{.Display}}</div>
            </div>
            {{if .Verbose}}
            <div>
              <small>verbose</small>
              <pre>{{.Verbose}}</pre>
            </div>
            {{end}}
            {{if .Logs}}
            {{ $item := . }}
            <div class="chart">
              <svg class="timeline" width="100%" height="34" viewBox="0 0 220 34" preserveAspectRatio="xMidYMid meet">
                <line x1="0" y1="17" x2="220" y2="17"></line>
                {{ $total := len .Logs }}
                {{range $i, $log := .Logs}}
                  {{ $cls := "" }}
                  {{if $log.Changed}}{{$cls = (printf "%s %s" $cls "changed")}}{{end}}
                  {{if $log.Error}}{{$cls = (printf "%s %s" $cls "error")}}{{end}}
                  <circle cx="{{chartX $i $total}}" cy="17" r="5" class="{{$cls}}">
                    <title>{{$log.At}}</title>
                  </circle>
				{{end}}
              </svg>
            </div>
            <details data-item="{{.Name}}" class="detail-box">
              <summary>Detail log (latest {{len .Logs}})</summary>
              {{if eq .Name "FileChangeCapturer"}}
                <div class="hint">Max files: {{.Meta.MaxFiles}}</div>
              {{end}}
              {{range .Logs}}
              <div class="hint">{{.At}}</div>
              {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
              {{if .Info}}<pre>{{.Info}}</pre>{{end}}
              {{if .Verbose}}<pre>{{.Verbose}}</pre>{{end}}
              {{end}}
            </details>
            {{end}}
            {{if .Logs}}
            {{end}}
          {{end}}
        </div>
      </div>
      {{end}}
    </div>
  </div>
</body>
<script>
  (function() {
    const autoBox = document.getElementById('autoRefresh');
    const eventBox = document.getElementById('eventRefresh');
    const input = document.getElementById('refreshInterval');
    const metaEl = document.querySelector('.meta');
    const gridEl = document.querySelector('.grid');
    const storageKeyAuto = 'miniedr:auto';
    const storageKeyAutoInterval = 'miniedr:autoInterval';
    const storageKeyEvent = 'miniedr:event';
    let timer = null;
    let es = null;

    async function refreshView() {
      const detailState = Array.from(document.querySelectorAll('.grid details')).map(el => ({
        name: el.getAttribute('data-item') || '',
        open: el.hasAttribute('open'),
        scroll: el.scrollTop,
      }));
      try {
        const res = await fetch(window.location.href, {cache: 'no-store'});
        const html = await res.text();
        const doc = new DOMParser().parseFromString(html, 'text/html');
        const newMeta = doc.querySelector('.meta');
        const newGrid = doc.querySelector('.grid');
        if (newMeta && metaEl) metaEl.innerHTML = newMeta.innerHTML;
        if (newGrid && gridEl) {
          gridEl.innerHTML = newGrid.innerHTML;
          detailState.forEach(state => {
            if (!state.name) return;
            const el = gridEl.querySelector('details[data-item="' + state.name + '"]');
            if (!el) return;
            if (state.open) {
              el.setAttribute('open', '');
            }
            el.scrollTop = state.scroll;
          });
        }
      } catch (e) {
        console.error('refresh failed', e);
      }
    }

    function applyAuto() {
      clearInterval(timer);
      const enabled = autoBox.checked;
      let sec = parseInt(input.value, 10);
      if (isNaN(sec) || sec <= 0) sec = {{.RefreshSecs}};
      input.value = sec;

      localStorage.setItem(storageKeyAuto, enabled ? '1' : '0');
      localStorage.setItem(storageKeyAutoInterval, sec.toString());

      if (enabled) {
        timer = setInterval(refreshView, sec * 1000);
      }
    }

    function stopEventStream() {
      if (es) {
        es.close();
        es = null;
      }
    }

    function applyEvent() {
      const enabled = eventBox.checked;
      localStorage.setItem(storageKeyEvent, enabled ? '1' : '0');
      stopEventStream();
      if (enabled) {
        es = new EventSource('/events');
        es.onmessage = refreshView;
        es.onerror = () => stopEventStream();
      }
    }

    // Restore user prefs
    const savedAuto = localStorage.getItem(storageKeyAuto);
    if (savedAuto !== null) {
      autoBox.checked = savedAuto === '1';
    }
    const savedEvent = localStorage.getItem(storageKeyEvent);
    if (savedEvent !== null) {
      eventBox.checked = savedEvent === '1';
    }
    const savedSec = localStorage.getItem(storageKeyAutoInterval);
    if (savedSec !== null && savedSec !== '') {
      input.value = savedSec;
    }

    autoBox.addEventListener('change', applyAuto);
    eventBox.addEventListener('change', applyEvent);
    input.addEventListener('change', applyAuto);
    document.getElementById('refreshBtn').onclick = refreshView;

    applyAuto();
    applyEvent();
  })();
</script>
</html>
`

func divMS(ms int64) float64 {
	if ms <= 0 {
		return 0
	}
	return float64(ms) / 1000.0
}

func minCapturerInterval(cs capturer.Capturers) time.Duration {
	var min time.Duration
	for _, c := range cs {
		iv := capturer.DefaultIntervalFor(c)
		if iv <= 0 {
			continue
		}
		if min == 0 || iv < min {
			min = iv
		}
	}
	if min == 0 {
		min = 5 * time.Second
	}
	return min
}

var tsRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+\-]\d{2}:?\d{2})`)

// normalizePayload removes timestamp-like tokens so change detection ignores time.
func normalizePayload(s string) string {
	out := tsRe.ReplaceAllString(s, "<ts>")
	return out
}

func metricsFingerprint(m map[string]float64) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%.4f;", k, m[k])
	}
	return b.String()
}

func metricVal(m map[string]float64, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key]
	return v, ok
}

func metricInt(m map[string]float64, key string) int {
	if v, ok := metricVal(m, key); ok {
		return int(v)
	}
	return -1
}

// updateNetScale keeps a per-capturer dynamic scale for NET graphs.
func (d *DashboardServer) updateNetScale(name string, info capturer.InfoData) float64 {
	rx, tx, ok := extractRatesMetrics(info)
	if !ok {
		return 0
	}
	total := rx + tx
	if total <= 0 {
		return 0
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.netScales == nil {
		d.netScales = make(map[string]float64)
	}
	scale := d.netScales[name]
	if scale <= 0 {
		scale = autoNetScale(total)
	}
	if total > scale {
		scale = total
	}
	// Decay scale when traffic is much lower to keep gauge responsive.
	if total < scale/3 {
		scale = scale * 0.7
		if scale < 256*1024 {
			scale = 256 * 1024
		}
	}
	d.netScales[name] = scale
	return scale
}

func (d *DashboardServer) updateCountScale(itemName, label string, val float64) float64 {
	if val < 0 {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	{
		if d.countScales == nil {
			d.countScales = make(map[string]map[string]float64)
		}
		lmap := d.countScales[itemName]
		if lmap == nil {
			lmap = make(map[string]float64)
			d.countScales[itemName] = lmap
		}
		scale := lmap[label]
		if scale <= 0 {
			scale = autoCountScale(val)
		}
		if val > scale {
			scale = val
		}
		lmap[label] = scale
		return scale
	}
}

func extractRatesMetrics(info capturer.InfoData) (float64, float64, bool) {
	rx, rxOk := metricVal(info.Metrics, "net.rx_bytes_per_sec")
	tx, txOk := metricVal(info.Metrics, "net.tx_bytes_per_sec")
	if rxOk && txOk {
		return rx, tx, true
	}
	if rxStr, txStr, ok := extractRates(info.Summary); ok {
		return rxStr, txStr, true
	}
	return 0, 0, false
}

func chartX(idx, total int) int {
	if total <= 1 {
		return 0
	}
	span := 220.0
	step := span / float64(total-1)
	x := math.Round(step * float64(idx))
	if x < 0 {
		x = 0
	}
	if x > span {
		x = span
	}
	return int(x)
}

func deriveGraphs(name string, info capturer.InfoData, netScale float64, countScale func(label string, val float64) float64) []graphInfo {
	up := strings.ToUpper(name)
	var gs []graphInfo
	if countScale == nil {
		countScale = func(_ string, _ float64) float64 { return 0 }
	}
	switch {
	case strings.Contains(up, "MEM"):
		if pct, ok := metricVal(info.Metrics, "mem.ram.used_pct"); ok {
			gs = append(gs, graphInfo{Label: "RAM used", Value: pct, Display: clampGraphValue(pct), ValueText: fmt.Sprintf("%.1f%%", pct)})
		} else if pct, ok := extractPercent(info.Summary, `UsedApprox=[^()]*\(([\d\.]+)%\)`); ok {
			gs = append(gs, graphInfo{Label: "RAM used", Value: pct, Display: clampGraphValue(pct), ValueText: fmt.Sprintf("%.1f%%", pct)})
		}
		if pct, ok := metricVal(info.Metrics, "mem.swap.used_pct"); ok {
			gs = append(gs, graphInfo{Label: "Swap used", Value: pct, Display: clampGraphValue(pct), ValueText: fmt.Sprintf("%.1f%%", pct)})
		} else if pct, ok := extractPercent(info.Summary, `Swap: Total=[\d]+B Used=[\d]+B \(([\d\.]+)%\)`); ok {
			gs = append(gs, graphInfo{Label: "Swap used", Value: pct, Display: clampGraphValue(pct), ValueText: fmt.Sprintf("%.1f%%", pct)})
		}
	case strings.Contains(up, "CPU"):
		if pct, ok := metricVal(info.Metrics, "cpu.total_pct"); ok {
			gs = append(gs, graphInfo{Label: "CPU avg", Value: pct, Display: clampGraphValue(pct), ValueText: fmt.Sprintf("%.1f%%", pct)})
		} else if pct, ok := extractPercent(info.Summary, `totalUsage=([\d\.]+)%`); ok {
			gs = append(gs, graphInfo{Label: "CPU avg", Value: pct, Display: clampGraphValue(pct), ValueText: fmt.Sprintf("%.1f%%", pct)})
		}
	case strings.Contains(up, "DISK"):
		if pct, ok := metricVal(info.Metrics, "disk.used_pct"); ok {
			gs = append(gs, graphInfo{Label: "DISK used", Value: pct, Display: clampGraphValue(pct), ValueText: fmt.Sprintf("%.1f%%", pct)})
		} else if pct, ok := extractPercent(info.Summary, `used=([\d\.]+)%`); ok {
			gs = append(gs, graphInfo{Label: "DISK used", Value: pct, Display: clampGraphValue(pct), ValueText: fmt.Sprintf("%.1f%%", pct)})
		}
	case strings.Contains(up, "NET"):
		rx, rxOk := metricVal(info.Metrics, "net.rx_bytes_per_sec")
		tx, txOk := metricVal(info.Metrics, "net.tx_bytes_per_sec")
		if !rxOk || !txOk {
			if rxStr, txStr, ok := extractRates(info.Summary); ok {
				rx = rxStr
				tx = txStr
				rxOk, txOk = true, true
			}
		}
		if rxOk && txOk {
			total := rx + tx
			scale := netScale
			if scale <= 0 {
				scale = autoNetScale(total)
			}
			pct := (total / scale) * 100
			if pct > 100 {
				pct = 100
			}
			pct = clampGraphValue(pct)
			rateText := fmt.Sprintf("%s/s", humanBytes(total))
			gs = append(gs, graphInfo{
				Label:     "NET\nthroughput",
				Value:     pct,
				Display:   pct,
				ValueText: rateText,
				MaxHint:   fmt.Sprintf("Max=%s/s", humanBytes(scale)),
			})
		}
	case strings.Contains(up, "CONN"):
		total := metricInt(info.Metrics, "conn.total")
		newCnt := metricInt(info.Metrics, "conn.new")
		deadCnt := metricInt(info.Metrics, "conn.dead")
		if total == -1 {
			total, _ = extractInt(info.Summary, `conns=([\d]+)`)
		}
		if newCnt == -1 {
			newCnt, _ = extractInt(info.Summary, `new=([\d]+)`)
		}
		if deadCnt == -1 {
			deadCnt, _ = extractInt(info.Summary, `dead=([\d]+)`)
		}
		if total >= 0 {
			scale := countScale("Conns", float64(total))
			gs = append(gs, countGauge("Conns", total, scale))
		}
		if newCnt >= 0 {
			scale := countScale("New conns", float64(newCnt))
			gs = append(gs, countGauge("New conns", newCnt, scale))
		}
		if deadCnt >= 0 {
			scale := countScale("Closed", float64(deadCnt))
			gs = append(gs, countGauge("Closed", deadCnt, scale))
		}
	case strings.Contains(up, "FILECHANGE"):
		v := metricInt(info.Metrics, "file.events")
		if v == -1 {
			if parsed, ok := extractInt(info.Summary, `events=([\d]+)`); ok {
				v = parsed
			}
		}
		if v >= 0 {
			scale := countScale("File events", float64(v))
			gs = append(gs, countGauge("File events", v, scale))
		}
	case strings.Contains(up, "PROC"):
		total := metricInt(info.Metrics, "proc.total")
		newCnt := metricInt(info.Metrics, "proc.new")
		deadCnt := metricInt(info.Metrics, "proc.dead")
		if total == -1 {
			total, _ = extractInt(info.Summary, `procs=([\d]+)`)
		}
		if newCnt == -1 {
			newCnt, _ = extractInt(info.Summary, `new=([\d]+)`)
		}
		if deadCnt == -1 {
			deadCnt, _ = extractInt(info.Summary, `dead=([\d]+)`)
		}
		if total >= 0 {
			scale := countScale("Procs", float64(total))
			gs = append(gs, countGauge("Procs", total, scale))
		}
		if newCnt >= 0 {
			scale := countScale("New procs", float64(newCnt))
			gs = append(gs, countGauge("New procs", newCnt, scale))
		}
		if deadCnt >= 0 {
			scale := countScale("Dead procs", float64(deadCnt))
			gs = append(gs, countGauge("Dead procs", deadCnt, scale))
		}
	case strings.Contains(up, "PERSIST"):
		added := metricInt(info.Metrics, "persist.added")
		changed := metricInt(info.Metrics, "persist.changed")
		removed := metricInt(info.Metrics, "persist.removed")
		if added == -1 {
			if v, ok := extractInt(info.Summary, `added=([\d]+)`); ok {
				added = v
			}
		}
		if changed == -1 {
			if v, ok := extractInt(info.Summary, `changed=([\d]+)`); ok {
				changed = v
			}
		}
		if removed == -1 {
			if v, ok := extractInt(info.Summary, `removed=([\d]+)`); ok {
				removed = v
			}
		}
		if added >= 0 {
			scale := countScale("Added", float64(added))
			gs = append(gs, countGauge("Added", added, scale))
		}
		if changed >= 0 {
			scale := countScale("Changed", float64(changed))
			gs = append(gs, countGauge("Changed", changed, scale))
		}
		if removed >= 0 {
			scale := countScale("Removed", float64(removed))
			gs = append(gs, countGauge("Removed", removed, scale))
		}
	}
	return gs
}

func extractPercent(s, pattern string) (float64, bool) {
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(s)
	if len(m) != 2 {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return v, true
}

func extractInt(s, pattern string) (int, bool) {
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(s)
	if len(m) != 2 {
		return -1, false
	}
	v, err := strconv.Atoi(m[1])
	if err != nil {
		return -1, false
	}
	return v, true
}

func countGauge(label string, val int, scale float64) graphInfo {
	v := float64(val)
	if scale <= 0 {
		scale = autoCountScale(v)
	}
	pct := 0.0
	if scale > 0 {
		pct = (v / scale) * 100
	}
	return graphInfo{
		Label:     label,
		Value:     v,
		Display:   clampGraphValue(pct),
		ValueText: formatCount(val),
		MaxHint:   fmt.Sprintf("max=%s", formatCount(int(scale))),
	}
}

func formatCount(v int) string {
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(v)/1_000_000)
	case v >= 1_000:
		return fmt.Sprintf("%.1fk", float64(v)/1_000)
	default:
		return fmt.Sprintf("%d", v)
	}
}

func autoCountScale(v float64) float64 {
	switch {
	case v >= 100_000:
		return 120_000
	case v >= 50_000:
		return 60_000
	case v >= 10_000:
		return 12_000
	case v >= 5_000:
		return 6_000
	case v >= 1_000:
		return 1_200
	case v >= 500:
		return 700
	case v >= 200:
		return 250
	case v >= 100:
		return 120
	case v >= 50:
		return 70
	case v >= 10:
		return 15
	default:
		if v <= 0 {
			return 10
		}
		return math.Max(v, 10)
	}
}

func clampGraphValue(v float64) float64 {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	if v > 0 && v < 1 {
		return 1 // ensure visibility for tiny non-zero values
	}
	return v
}

func extractRates(info string) (float64, float64, bool) {
	re := regexp.MustCompile(`rxRate=(\d+)B/s, txRate=(\d+)B/s`)
	m := re.FindStringSubmatch(info)
	if len(m) != 3 {
		return 0, 0, false
	}
	rx, err1 := strconv.ParseFloat(m[1], 64)
	tx, err2 := strconv.ParseFloat(m[2], 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return rx, tx, true
}

func humanBytes(v float64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case v >= gb:
		return fmt.Sprintf("%.1fGB", v/gb)
	case v >= mb:
		return fmt.Sprintf("%.1fMB", v/mb)
	case v >= kb:
		return fmt.Sprintf("%.1fKB", v/kb)
	default:
		return fmt.Sprintf("%.0fB", v)
	}
}

func summarizeInfo(name string, info capturer.InfoData) string {
	up := strings.ToUpper(name)
	m := info.Metrics
	summary := info.Summary
	switch {
	case strings.Contains(up, "CPU"):
		var parts []string
		if avg, ok := metricVal(m, "cpu.total_pct"); ok {
			parts = append(parts, fmt.Sprintf("Avg %.2f%%", avg))
		} else if avg := captureGroup(summary, `totalUsage=([\d\.]+)%`); avg != "" {
			parts = append(parts, fmt.Sprintf("Avg %s%%", avg))
		}
		// Show up to 4 cores from metrics
		for i := 0; i < 4; i++ {
			if core, ok := metricVal(m, fmt.Sprintf("cpu.core%d_pct", i)); ok {
				parts = append(parts, fmt.Sprintf("cpu%d=%.1f%%", i, core))
			}
		}
		if len(parts) == 0 {
			return summary
		}
		return strings.Join(parts, " · ")
	case strings.Contains(up, "MEM"):
		var parts []string
		if used, ok := metricVal(m, "mem.ram.used_pct"); ok {
			parts = append(parts, fmt.Sprintf("RAM %.2f%%", used))
		} else if used := captureGroup(summary, `UsedApprox=[^()]*\(([\d\.]+)%\)`); used != "" {
			parts = append(parts, fmt.Sprintf("RAM %s%%", used))
		}
		if total, ok := metricVal(m, "mem.ram.total_bytes"); ok {
			parts = append(parts, fmt.Sprintf("Total %s", humanBytes(total)))
		} else if total := captureGroup(summary, `RAM: Total=([\d]+)B`); total != "" {
			parts = append(parts, fmt.Sprintf("Total %s", humanBytesString(total)))
		}
		swapUsedVal, hasSwapUsed := metricVal(m, "mem.swap.used_bytes")
		swapPctVal, hasSwapPct := metricVal(m, "mem.swap.used_pct")
		swapUsedStr, swapPctStr := captureGroup2(summary, `Swap: Total=[\d]+B Used=([\d]+)B \(([\d\.]+)%\)`)
		if hasSwapUsed {
			if hasSwapPct {
				parts = append(parts, fmt.Sprintf("Swap %s (%.2f%%)", humanBytes(swapUsedVal), swapPctVal))
			} else {
				parts = append(parts, fmt.Sprintf("Swap %s", humanBytes(swapUsedVal)))
			}
		} else if swapUsedStr != "" {
			parts = append(parts, fmt.Sprintf("Swap %s (%s%%)", humanBytesString(swapUsedStr), swapPctStr))
		}
		if len(parts) == 0 {
			return summary
		}
		return strings.Join(parts, " · ")
	case strings.Contains(up, "DISK"):
		var parts []string
		if used, ok := metricVal(m, "disk.used_pct"); ok {
			parts = append(parts, fmt.Sprintf("Used %.2f%%", used))
		} else if used := captureGroup(summary, `used=([\d\.]+)%`); used != "" {
			parts = append(parts, fmt.Sprintf("Used %s%%", used))
		}
		rRate, rOk := metricVal(m, "disk.read_bytes_per_sec")
		wRate, wOk := metricVal(m, "disk.write_bytes_per_sec")
		if rOk || wOk {
			parts = append(parts, fmt.Sprintf("ioRate=read %s/s write %s/s", humanBytes(rRate), humanBytes(wRate)))
		} else if io := captureGroup(summary, `ioRate=([^,]+)`); io != "" {
			parts = append(parts, io)
		}
		if len(parts) == 0 {
			return summary
		}
		return strings.Join(parts, " · ")
	case strings.Contains(up, "NET"):
		var parts []string
		if rx, ok := metricVal(m, "net.rx_bytes_per_sec"); ok {
			parts = append(parts, fmt.Sprintf("RX %s/s", humanBytes(rx)))
		} else if rx := captureGroup(summary, `rxRate=([^,]+)`); rx != "" {
			parts = append(parts, "RX "+rx)
		}
		if tx, ok := metricVal(m, "net.tx_bytes_per_sec"); ok {
			parts = append(parts, fmt.Sprintf("TX %s/s", humanBytes(tx)))
		} else if tx := captureGroup(summary, `txRate=([^\)]+)`); tx != "" {
			parts = append(parts, "TX "+tx)
		}
		if len(parts) == 0 {
			return summary
		}
		return strings.Join(parts, " · ")
	case strings.Contains(up, "FILECHANGE"):
		var parts []string
		if ev, ok := metricVal(m, "file.events"); ok {
			parts = append(parts, fmt.Sprintf("Events %d", int(ev)))
		} else if ev := captureGroup(summary, `events=([\d]+)`); ev != "" {
			parts = append(parts, "Events "+ev)
		}
		if files, ok := metricVal(m, "file.files"); ok {
			parts = append(parts, fmt.Sprintf("Files %d", int(files)))
		} else if files := captureGroup(summary, `files=([\d]+)`); files != "" {
			parts = append(parts, "Files "+files)
		}
		if len(parts) == 0 {
			return summary
		}
		return strings.Join(parts, " · ")
	case strings.Contains(up, "CONN"):
		total := metricInt(m, "conn.total")
		newCnt := metricInt(m, "conn.new")
		deadCnt := metricInt(m, "conn.dead")
		var parts []string
		if total >= 0 {
			parts = append(parts, fmt.Sprintf("Conns %d", total))
		}
		if newCnt >= 0 {
			parts = append(parts, fmt.Sprintf("New %d", newCnt))
		}
		if deadCnt >= 0 {
			parts = append(parts, fmt.Sprintf("Closed %d", deadCnt))
		}
		if len(parts) == 0 {
			return summary
		}
		return strings.Join(parts, " · ")
	case strings.Contains(up, "PROC"):
		total := metricInt(m, "proc.total")
		newCnt := metricInt(m, "proc.new")
		deadCnt := metricInt(m, "proc.dead")
		var parts []string
		if total >= 0 {
			parts = append(parts, fmt.Sprintf("Procs %d", total))
		}
		if newCnt >= 0 {
			parts = append(parts, fmt.Sprintf("New %d", newCnt))
		}
		if deadCnt >= 0 {
			parts = append(parts, fmt.Sprintf("Dead %d", deadCnt))
		}
		if len(parts) == 0 {
			return summary
		}
		return strings.Join(parts, " · ")
	case strings.Contains(up, "PERSIST"):
		added := metricInt(m, "persist.added")
		changed := metricInt(m, "persist.changed")
		removed := metricInt(m, "persist.removed")
		var parts []string
		if added >= 0 {
			parts = append(parts, fmt.Sprintf("Added %d", added))
		}
		if changed >= 0 {
			parts = append(parts, fmt.Sprintf("Changed %d", changed))
		}
		if removed >= 0 {
			parts = append(parts, fmt.Sprintf("Removed %d", removed))
		}
		if len(parts) == 0 {
			return summary
		}
		return strings.Join(parts, " · ")
	default:
		return summary
	}
}

func captureGroup(s, pattern string) string {
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(s)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

func captureGroup2(s, pattern string) (string, string) {
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(s)
	if len(m) >= 3 {
		return m[1], m[2]
	}
	return "", ""
}

func humanBytesString(s string) string {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return ""
	}
	return humanBytes(v)
}
func autoNetScale(total float64) float64 {
	// auto-scale to a near-upper bucket so gauges remain readable
	switch {
	case total >= 100*1024*1024:
		return 100 * 1024 * 1024
	case total >= 50*1024*1024:
		return 50 * 1024 * 1024
	case total >= 10*1024*1024:
		return 10 * 1024 * 1024
	case total >= 5*1024*1024:
		return 5 * 1024 * 1024
	case total >= 1*1024*1024:
		return 1 * 1024 * 1024
	case total >= 512*1024:
		return 512 * 1024
	default:
		return 256 * 1024
	}
}
