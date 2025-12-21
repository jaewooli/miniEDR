package dashboard

import (
	"context"
	"encoding/json"
	_ "embed"
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

	"github.com/jaewooli/miniedr"
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
	alertHistory map[string][]dashboardAlertEntry
	globalAlerts  []dashboardAlertEntry
	ruleConfig    RuleConfig
	detector      *miniedr.Detector
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
	Alerts  []miniedr.Alert
	AlertHistory []dashboardAlertEntry
}

type dashboardLogEntry struct {
	At      string
	Info    string
	Verbose string
	Error   string
	Changed bool
	Warming bool
}

type dashboardAlertEntry struct {
	At       string
	Severity miniedr.AlertSeverity
	Title    string
	Message  string
	RuleID   string
	Source   string
}

type graphInfo struct {
	Label     string
	Value     float64
	Display   float64
	ValueText string
	MaxHint   string // optional hint about the scale (e.g., "scale=10MB/s")
}

const maxAlertHistory = 50

type dashboardData struct {
	Title             string
	RefreshedAt       string
	Items             []dashboardItem
	GlobalAlerts      []dashboardAlertEntry
	AutoRefresh       bool
	RefreshSecs       int
	EventRefresh      bool
	CaptureIntervalMS int64
	RuleConfig        RuleConfig
}

// RuleConfig holds adjustable thresholds for built-in rules.
type RuleConfig struct {
	CPUHighPct       float64 `json:"cpu_high_pct"`
	MemRamPct        float64 `json:"mem_ram_pct"`
	MemSwapPct       float64 `json:"mem_swap_pct"`
	ProcBurst        int     `json:"proc_burst"`
	NetSpikeBytes    float64 `json:"net_spike_bps"`
	FileEventsBurst  int     `json:"file_events"`
	PersistMinChange int     `json:"persist_changes"`
}

func NewDashboardServer(capturers capturer.Capturers, title string, verbose bool) *DashboardServer {
	t := template.Must(template.New("dashboard").Funcs(template.FuncMap{
		"divMS":  divMS,
		"chartX": chartX,
		"hasNL":  func(s string) bool { return strings.Contains(s, "\n") },
		"mbps":   func(b float64) float64 { return b / (1024 * 1024) },
	}).Parse(dashboardHTML))
	if title == "" {
		title = "miniEDR Dashboard"
	}
	displayInterval := minCapturerInterval(capturers)
	d := &DashboardServer{
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
		alertHistory:    make(map[string][]dashboardAlertEntry),
		globalAlerts:    []dashboardAlertEntry{},
		ruleConfig:      defaultRuleConfig(),
	}
	d.rebuildDetector()
	return d
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
	mux.HandleFunc("/rules", d.handleRules)

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

// handleRules exposes GET/POST to view or update rule thresholds.
func (d *DashboardServer) handleRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		d.mu.RLock()
		cfg := d.ruleConfig
		d.mu.RUnlock()
		_ = json.NewEncoder(w).Encode(cfg)
	case http.MethodPost:
		var req RuleConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("invalid json: %v", err), http.StatusBadRequest)
			return
		}
		cfg := normalizeRuleConfig(req)
		d.mu.Lock()
		d.ruleConfig = cfg
		d.rebuildDetectorLocked()
		if d.hasSnapshot {
			d.snapshot.RuleConfig = d.ruleConfig
		}
		out := d.ruleConfig
		d.mu.Unlock()
		_ = json.NewEncoder(w).Encode(out)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
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
	name := displayName(c)

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
			if info.Meta.AgentBuild == "" && info.Meta.AgentVersion != "" {
				info.Meta.AgentBuild = info.Meta.AgentVersion
			}
			if info.Meta.Capturer == "" {
				info.Meta.Capturer = name
			}
			if info.Meta.CapturedAt.IsZero() {
				info.Meta.CapturedAt = time.Now()
			}
			if info.Meta.Timezone == "" {
				info.Meta.Timezone = time.Now().Format("-0700")
			}
			item.Info = info
			item.Meta = info.Meta
			if vc, ok := c.(capturer.VerboseInfo); ok {
				verb, err := vc.GetVerboseInfo()
				if err != nil {
					item.Error = fmt.Sprintf("getverboseinfo error: %v", err)
				} else {
					item.Verbose = verb
				}
			}
			d.mu.RLock()
			det := d.detector
			d.mu.RUnlock()
			if det != nil {
				item.Alerts = det.Evaluate(info)
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

	history := append([]dashboardAlertEntry{}, d.alertHistory[name]...)
	if len(item.Alerts) > 0 {
		for _, alert := range item.Alerts {
			at := alert.At
			if at.IsZero() {
				at = time.Now()
			}
			source := alert.Source
			if source == "" {
				source = alert.Meta.Capturer
			}
			entry := dashboardAlertEntry{
				At:       at.Local().Format("2006-01-02 15:04:05"),
				Severity: alert.Severity,
				Title:    alert.Title,
				Message:  alert.Message,
				RuleID:   alert.RuleID,
				Source:   source,
			}
			history = append(history, dashboardAlertEntry{
				At:       entry.At,
				Severity: entry.Severity,
				Title:    entry.Title,
				Message:  entry.Message,
				RuleID:   entry.RuleID,
				Source:   entry.Source,
			})
			d.globalAlerts = append(d.globalAlerts, entry)
		}
		if len(history) > maxAlertHistory {
			history = history[len(history)-maxAlertHistory:]
		}
		d.alertHistory[name] = history
		if len(d.globalAlerts) > maxAlertHistory {
			d.globalAlerts = d.globalAlerts[len(d.globalAlerts)-maxAlertHistory:]
		}
	}
	item.AlertHistory = history

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
		GlobalAlerts:      append([]dashboardAlertEntry{}, d.globalAlerts...),
		AutoRefresh:       autoRefresh,
		RefreshSecs:       refreshSecs,
		EventRefresh:      eventRefresh,
		CaptureIntervalMS: capInterval.Milliseconds(),
		RuleConfig:        d.ruleConfig,
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
	name := displayName(c)
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

//go:embed dashboard.html
var dashboardHTML string

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

func defaultRuleConfig() RuleConfig {
	return RuleConfig{
		CPUHighPct:       90,
		MemRamPct:        90,
		MemSwapPct:       60,
		ProcBurst:        10,
		NetSpikeBytes:    1 * 1024 * 1024,
		FileEventsBurst:  50,
		PersistMinChange: 1,
	}
}

func normalizeRuleConfig(cfg RuleConfig) RuleConfig {
	def := defaultRuleConfig()
	out := cfg
	if out.CPUHighPct <= 0 {
		out.CPUHighPct = def.CPUHighPct
	}
	if out.MemRamPct <= 0 {
		out.MemRamPct = def.MemRamPct
	}
	if out.MemSwapPct <= 0 {
		out.MemSwapPct = def.MemSwapPct
	}
	if out.ProcBurst <= 0 {
		out.ProcBurst = def.ProcBurst
	}
	if out.NetSpikeBytes <= 0 {
		out.NetSpikeBytes = def.NetSpikeBytes
	}
	if out.FileEventsBurst <= 0 {
		out.FileEventsBurst = def.FileEventsBurst
	}
	if out.PersistMinChange <= 0 {
		out.PersistMinChange = def.PersistMinChange
	}
	return out
}

func (d *DashboardServer) rebuildDetector() {
	d.mu.Lock()
	d.rebuildDetectorLocked()
	d.mu.Unlock()
}

func (d *DashboardServer) rebuildDetectorLocked() {
	cfg := normalizeRuleConfig(d.ruleConfig)
	d.ruleConfig = cfg
	d.detector = &miniedr.Detector{
		Rules: []miniedr.RuleSpec{
			miniedr.RuleCPUHigh(cfg.CPUHighPct),
			miniedr.RuleMemPressure(cfg.MemRamPct, cfg.MemSwapPct),
			miniedr.RuleProcBurst(cfg.ProcBurst),
			miniedr.RuleNetSpike(cfg.NetSpikeBytes),
			miniedr.RuleFileEventBurst(cfg.FileEventsBurst),
			miniedr.RulePersistenceChange(cfg.PersistMinChange),
		},
		// Keep alert history per snapshot without dedup/rate-limit for the dashboard.
	}
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

func displayName(c capturer.Capturer) string {
	raw := capturer.CapturerName(c)
	trimmed := strings.TrimSuffix(raw, "Capturer")
	upper := strings.ToUpper(trimmed)
	if upper == "" {
		return raw
	}
	return upper
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
