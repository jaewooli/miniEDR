package miniedr

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"sort"
	"sync"
	"time"
)

// DashboardServer exposes a lightweight HTML dashboard to view capturer snapshots.
// It maintains a background capture loop and can push refresh events to the UI.
type DashboardServer struct {
	Capturers Capturers

	mu              sync.RWMutex
	tmpl            *template.Template
	nowFn           func() time.Time
	title           string
	verbose         bool
	autoRefresh     bool
	eventRefresh    bool
	refreshSeconds  int
	captureInterval time.Duration

	snapshot    dashboardData
	hasSnapshot bool
	clients     map[chan string]struct{}
	lastPayload map[string]string
}

type dashboardItem struct {
	Name    string
	Info    string
	Verbose string
	Error   string
	Changed bool
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

func NewDashboardServer(capturers Capturers, title string, verbose bool) *DashboardServer {
	t := template.Must(template.New("dashboard").Funcs(template.FuncMap{
		"divMS": divMS,
	}).Parse(dashboardHTML))
	if title == "" {
		title = "miniEDR Dashboard"
	}
	return &DashboardServer{
		Capturers:       capturers,
		tmpl:            t,
		nowFn:           time.Now,
		title:           title,
		verbose:         verbose,
		refreshSeconds:  10,
		eventRefresh:    true,
		captureInterval: 5 * time.Second,
		clients:         make(map[chan string]struct{}),
		lastPayload:     make(map[string]string),
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
	if interval <= 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.captureInterval = interval
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
	capInterval := d.captureInterval
	d.mu.RUnlock()

	items := make([]dashboardItem, 0, len(d.Capturers))
	for _, c := range d.Capturers {
		name := typeName(c)

		if err := c.Capture(); err != nil {
			items = append(items, dashboardItem{Name: name, Error: fmt.Sprintf("capture error: %v", err)})
			continue
		}

		info, err := c.GetInfo()
		if err != nil {
			items = append(items, dashboardItem{Name: name, Error: fmt.Sprintf("getinfo error: %v", err)})
			continue
		}

		item := dashboardItem{Name: name, Info: info}

		if verbose {
			if vc, ok := c.(VerboseInfo); ok {
				verb, err := vc.GetVerboseInfo()
				if err != nil {
					item.Error = fmt.Sprintf("getverboseinfo error: %v", err)
				} else {
					item.Verbose = verb
				}
			}
		}

		// change detection: compare serialized payload excluding timestamps
		payload := normalizePayload(item.Info) + "|" + normalizePayload(item.Verbose) + "|" + normalizePayload(item.Error)
		d.mu.RLock()
		prev := d.lastPayload[name]
		d.mu.RUnlock()
		if prev != "" && prev != payload {
			item.Changed = true
		}
		d.mu.Lock()
		d.lastPayload[name] = payload
		d.mu.Unlock()

		items = append(items, item)
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })

	ref := nowFn().Format(time.RFC3339)

	d.mu.Lock()
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
	d.mu.RUnlock()

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
  gap: 16px;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
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
.error {
  color: #fca5a5;
  font-weight: 600;
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
          </div>
          {{if .Error}}
            <div class="error">{{.Error}}</div>
          {{else}}
            <div>
              <small>info</small>
              <pre>{{.Info}}</pre>
            </div>
            {{if .Verbose}}
            <div>
              <small>verbose</small>
              <pre>{{.Verbose}}</pre>
            </div>
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
    const storageKeyAuto = 'miniedr:auto';
    const storageKeyAutoInterval = 'miniedr:autoInterval';
    const storageKeyEvent = 'miniedr:event';
    let timer = null;
    let es = null;

    function applyAuto() {
      clearInterval(timer);
      const enabled = autoBox.checked;
      let sec = parseInt(input.value, 10);
      if (isNaN(sec) || sec <= 0) sec = {{.RefreshSecs}};
      input.value = sec;

      localStorage.setItem(storageKeyAuto, enabled ? '1' : '0');
      localStorage.setItem(storageKeyAutoInterval, sec.toString());

      if (enabled) {
        timer = setInterval(() => location.reload(), sec * 1000);
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
        es.onmessage = () => location.reload();
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

var tsRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+\-]\d{2}:?\d{2})`)

// normalizePayload removes timestamp-like tokens so change detection ignores time.
func normalizePayload(s string) string {
	out := tsRe.ReplaceAllString(s, "<ts>")
	return out
}
