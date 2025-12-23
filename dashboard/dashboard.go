package dashboard

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"os"
	"regexp"
	"runtime"
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
	authToken       string
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
	alertHistory  map[string][]dashboardAlertEntry
	globalAlerts  []dashboardAlertEntry
	corrVersion   int64
	rulesConfig   RulesConfig
	rulesPath     string
	metricsPath   string
	defaultMetrics []string
	customMetrics []CustomMetric
	detector      *miniedr.Detector
	iocConfig     miniedr.IOCConfig
}

type dashboardItem struct {
	Name         string
	Info         capturer.InfoData
	Verbose      string
	Error        string
	Changed      bool
	Warming      bool
	Logs         []dashboardLogEntry
	Graphs       []graphInfo
	Display      string
	Meta         capturer.TelemetryMeta
	Alerts       []miniedr.Alert
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
	ID       string
	At       string
	AtTime   time.Time
	Severity miniedr.AlertSeverity
	Title    string
	Message  string
	RuleID   string
	Source   string
	Evidence string
	Meta     string
	Related  string
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
	RulesConfig       RulesConfig
}

// RuleDefinition defines a single metric threshold rule.
type RuleDefinition struct {
	ID       string                `json:"id"`
	Title    string                `json:"title"`
	Severity miniedr.AlertSeverity `json:"severity"`
	Metric   string                `json:"metric"`
	Op       string                `json:"op"`
	Value    float64               `json:"value"`
	Message  string                `json:"message"`
	Source   string                `json:"source,omitempty"`
	Enabled  bool                  `json:"enabled"`
}

// RulesConfig holds editable rules persisted to disk.
type RulesConfig struct {
	Rules []RuleDefinition `json:"rules"`
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
		rulesPath:       "rules.json",
		metricsPath:     "metrics.json",
	}
	d.rulesConfig = d.loadRulesConfig()
	d.customMetrics = d.loadCustomMetricsConfig()
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

// SetAuthToken configures a shared token required for dashboard access.
func (d *DashboardServer) SetAuthToken(token string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.authToken = strings.TrimSpace(token)
}

// SetIOCConfig enables IOC matching alerts in the dashboard detector.
func (d *DashboardServer) SetIOCConfig(cfg miniedr.IOCConfig) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.iocConfig = cfg
	d.rebuildDetectorLocked()
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
	mux.HandleFunc("/metrics", d.handleMetrics)

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

const authCookieName = "miniedr_token"

func (d *DashboardServer) authorize(w http.ResponseWriter, r *http.Request) bool {
	d.mu.RLock()
	token := d.authToken
	d.mu.RUnlock()
	if token == "" {
		return true
	}
	reqToken, fromQuery := extractAuthToken(r)
	if reqToken == "" || reqToken != token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if fromQuery {
		http.SetCookie(w, &http.Cookie{
			Name:     authCookieName,
			Value:    reqToken,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	}
	return true
}

func extractAuthToken(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	if token := strings.TrimSpace(r.Header.Get("X-EDR-Token")); token != "" {
		return token, false
	}
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:]), false
	}
	if cookie, err := r.Cookie(authCookieName); err == nil {
		if token := strings.TrimSpace(cookie.Value); token != "" {
			return token, false
		}
	}
	if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
		return token, true
	}
	return "", false
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
	if !d.authorize(w, r) {
		return
	}
	d.handleDashboard(w, r)
}

// handleRules exposes GET/POST to view or update rule configuration.
func (d *DashboardServer) handleRules(w http.ResponseWriter, r *http.Request) {
	if !d.authorize(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		d.mu.RLock()
		cfg := d.rulesConfig
		d.mu.RUnlock()
		_ = json.NewEncoder(w).Encode(cfg)
	case http.MethodPost:
		var req RulesConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("invalid json: %v", err), http.StatusBadRequest)
			return
		}
		cfg := normalizeRulesConfig(req)
		d.mu.Lock()
		d.rulesConfig = cfg
		d.rebuildDetectorLocked()
		if d.hasSnapshot {
			d.snapshot.RulesConfig = d.rulesConfig
		}
		out := d.rulesConfig
		d.mu.Unlock()
		if err := d.saveRulesConfig(cfg); err != nil {
			http.Error(w, fmt.Sprintf("save rules: %v", err), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (d *DashboardServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !d.authorize(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		d.mu.RLock()
		defaults := append([]string{}, d.defaultMetrics...)
		customs := append([]CustomMetric{}, d.customMetrics...)
		d.mu.RUnlock()
		payload := metricsPayload{
			Default: defaults,
			Custom:  toCustomMetricPayload(customs),
			All:     mergeMetrics(defaults, customMetricNames(customs)),
		}
		_ = json.NewEncoder(w).Encode(payload)
	case http.MethodPost:
		var req metricsPayload
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("invalid json: %v", err), http.StatusBadRequest)
			return
		}
		custom, err := normalizeCustomMetrics(req.Custom)
		if err != nil {
			http.Error(w, fmt.Sprintf("invalid metrics: %v", err), http.StatusBadRequest)
			return
		}
		if err := d.saveCustomMetricsConfig(custom); err != nil {
			http.Error(w, fmt.Sprintf("save metrics: %v", err), http.StatusInternalServerError)
			return
		}
		d.mu.Lock()
		d.customMetrics = custom
		defaults := append([]string{}, d.defaultMetrics...)
		d.mu.Unlock()
		payload := metricsPayload{
			Default: defaults,
			Custom:  toCustomMetricPayload(custom),
			All:     mergeMetrics(defaults, customMetricNames(custom)),
		}
		_ = json.NewEncoder(w).Encode(payload)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

type metricsPayload struct {
	Default []string              `json:"default"`
	Custom  []CustomMetricPayload `json:"custom"`
	All     []string              `json:"all"`
}

type CustomMetricPayload struct {
	Name string `json:"name"`
	Expr string `json:"expr"`
}

type CustomMetric struct {
	Name string     `json:"name"`
	Expr MetricExpr `json:"expr"`
}

type MetricExpr struct {
	Op     string       `json:"op,omitempty"`
	Args   []MetricExpr `json:"args,omitempty"`
	Metric string       `json:"metric,omitempty"`
	Value  *float64     `json:"value,omitempty"`
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
			d.mu.RLock()
			customs := append([]CustomMetric{}, d.customMetrics...)
			d.mu.RUnlock()
			applyCustomMetrics(&info, customs)
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
			evidenceJSON := "{}"
			if alert.Evidence != nil {
				if b, err := json.Marshal(alert.Evidence); err == nil {
					evidenceJSON = string(b)
				}
			}
			metaJSON := "{}"
			if b, err := json.Marshal(alert.Meta); err == nil {
				metaJSON = string(b)
			}
			relatedJSON := "[]"
			if len(alert.Correlated) > 0 {
				if b, err := json.Marshal(alert.Correlated); err == nil {
					relatedJSON = string(b)
				}
			}
			entry := dashboardAlertEntry{
				ID:       alert.ID,
				At:       at.Local().Format("2006-01-02 15:04:05"),
				AtTime:   at,
				Severity: alert.Severity,
				Title:    alert.Title,
				Message:  alert.Message,
				RuleID:   alert.RuleID,
				Source:   source,
				Evidence: evidenceJSON,
				Meta:     metaJSON,
				Related:  relatedJSON,
			}
			history = append(history, dashboardAlertEntry{
				ID:       entry.ID,
				At:       entry.At,
				AtTime:   entry.AtTime,
				Severity: entry.Severity,
				Title:    entry.Title,
				Message:  entry.Message,
				RuleID:   entry.RuleID,
				Source:   entry.Source,
				Evidence: entry.Evidence,
				Meta:     entry.Meta,
				Related:  entry.Related,
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

	if len(d.items) >= len(d.Capturers) {
		derived := deriveDefaultMetrics(items)
		if len(d.defaultMetrics) == 0 {
			d.defaultMetrics = derived
		} else if len(derived) > 0 {
			d.defaultMetrics = mergeMetrics(d.defaultMetrics, derived)
		}
	}

	globalAlerts := append([]dashboardAlertEntry{}, d.globalAlerts...)
	intervals := make(map[string]time.Duration, len(d.itemIntervals))
	for k, v := range d.itemIntervals {
		intervals[k] = v
	}
	d.corrVersion++
	corrVersion := d.corrVersion
	d.snapshot = dashboardData{
		Title:             title,
		RefreshedAt:       ref,
		Items:             items,
		GlobalAlerts:      globalAlerts,
		AutoRefresh:       autoRefresh,
		RefreshSecs:       refreshSecs,
		EventRefresh:      eventRefresh,
		CaptureIntervalMS: capInterval.Milliseconds(),
		RulesConfig:       d.rulesConfig,
	}
	d.hasSnapshot = true
	d.mu.Unlock()

	d.broadcast(ref)
	d.kickoffCorrelation(globalAlerts, intervals, d.displayInterval, corrVersion, ref)
}

func (d *DashboardServer) currentSnapshot() dashboardData {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.snapshot
}

func (d *DashboardServer) kickoffCorrelation(entries []dashboardAlertEntry, intervals map[string]time.Duration, defaultInterval time.Duration, version int64, ref string) {
	if len(entries) == 0 {
		return
	}
	entriesCopy := append([]dashboardAlertEntry{}, entries...)
	go func() {
		correlated := correlateAlerts(entriesCopy, intervals, defaultInterval)
		d.mu.Lock()
		if d.corrVersion != version {
			d.mu.Unlock()
			return
		}
		snap := d.snapshot
		snap.GlobalAlerts = correlated
		d.snapshot = snap
		d.hasSnapshot = true
		d.mu.Unlock()
		d.broadcast(ref)
	}()
}

func correlateAlerts(entries []dashboardAlertEntry, intervals map[string]time.Duration, defaultInterval time.Duration) []dashboardAlertEntry {
	if len(entries) == 0 {
		return entries
	}
	if intervals == nil {
		intervals = make(map[string]time.Duration)
	}
	maxInterval := defaultInterval
	if maxInterval <= 0 {
		maxInterval = time.Second
	}
	for _, v := range intervals {
		if v > maxInterval {
			maxInterval = v
		}
	}
	idxs := make([]int, len(entries))
	for i := range entries {
		idxs[i] = i
	}
	sort.Slice(idxs, func(i, j int) bool {
		return entries[idxs[i]].AtTime.Before(entries[idxs[j]].AtTime)
	})
	related := make([][]string, len(entries))
	for a := 0; a < len(idxs); a++ {
		i := idxs[a]
		atI := entries[i].AtTime
		for b := a + 1; b < len(idxs); b++ {
			j := idxs[b]
			if entries[j].AtTime.After(atI.Add(maxInterval)) {
				break
			}
			if !alertsOverlap(entries[i], entries[j], intervals, defaultInterval) {
				continue
			}
			if entries[i].ID != "" && entries[j].ID != "" {
				related[i] = append(related[i], entries[j].ID)
				related[j] = append(related[j], entries[i].ID)
			}
		}
	}

	out := make([]dashboardAlertEntry, len(entries))
	for i, entry := range entries {
		out[i] = entry
		out[i].Related = mergeRelatedJSON(entry.Related, related[i])
	}
	return out
}

func alertsOverlap(a, b dashboardAlertEntry, intervals map[string]time.Duration, defaultInterval time.Duration) bool {
	if a.AtTime.IsZero() || b.AtTime.IsZero() {
		return false
	}
	ia := intervalForAlert(a, intervals, defaultInterval)
	ib := intervalForAlert(b, intervals, defaultInterval)
	aAt := a.AtTime.Truncate(time.Second)
	bAt := b.AtTime.Truncate(time.Second)
	aStart := aAt.Add(-ia)
	bStart := bAt.Add(-ib)
	start := aStart
	if bStart.After(start) {
		start = bStart
	}
	end := aAt
	if bAt.Before(end) {
		end = bAt
	}
	return end.Sub(start) >= time.Second
}

func intervalForAlert(entry dashboardAlertEntry, intervals map[string]time.Duration, defaultInterval time.Duration) time.Duration {
	interval := intervals[entry.Source]
	if interval <= 0 {
		interval = defaultInterval
	}
	if interval <= 0 {
		interval = time.Second
	}
	return interval
}

func mergeRelatedJSON(existing string, derived []string) string {
	merged := make([]string, 0, len(derived))
	seen := make(map[string]struct{})
	if existing != "" {
		var list []string
		if err := json.Unmarshal([]byte(existing), &list); err == nil {
			for _, v := range list {
				if v == "" {
					continue
				}
				if _, ok := seen[v]; ok {
					continue
				}
				seen[v] = struct{}{}
				merged = append(merged, v)
			}
		}
	}
	for _, v := range derived {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		merged = append(merged, v)
	}
	if len(merged) == 0 {
		return "[]"
	}
	b, err := json.Marshal(merged)
	if err != nil {
		return "[]"
	}
	return string(b)
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
	if !d.authorize(w, r) {
		return
	}
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

func defaultRulesConfig() RulesConfig {
	return RulesConfig{
		Rules: []RuleDefinition{
			{
				ID:       "cpu.high_usage",
				Title:    "High CPU usage",
				Severity: miniedr.SeverityMedium,
				Metric:   "cpu.total_pct",
				Op:       ">=",
				Value:    90,
				Message:  "CPU usage {value}% exceeds threshold {threshold}%",
				Source:   "CPU",
				Enabled:  true,
			},
			{
				ID:       "mem.high_usage",
				Title:    "High RAM usage",
				Severity: miniedr.SeverityMedium,
				Metric:   "mem.ram.used_pct",
				Op:       ">=",
				Value:    90,
				Message:  "RAM usage {value}% exceeds {threshold}%",
				Source:   "MEM",
				Enabled:  true,
			},
			{
				ID:       "mem.swap_pressure",
				Title:    "Swap pressure",
				Severity: miniedr.SeverityLow,
				Metric:   "mem.swap.used_pct",
				Op:       ">=",
				Value:    60,
				Message:  "Swap usage {value}% exceeds {threshold}%",
				Source:   "MEM",
				Enabled:  true,
			},
			{
				ID:       "proc.burst",
				Title:    "Process burst",
				Severity: miniedr.SeverityMedium,
				Metric:   "proc.new",
				Op:       ">=",
				Value:    10,
				Message:  "{value} new processes detected (limit {threshold})",
				Source:   "PROC",
				Enabled:  true,
			},
			{
				ID:       "net.spike",
				Title:    "Network spike",
				Severity: miniedr.SeverityLow,
				Metric:   "net.total_bytes_per_sec",
				Op:       ">=",
				Value:    1 * 1024 * 1024,
				Message:  "Network throughput {value}B/s exceeds {threshold}B/s",
				Source:   "NET",
				Enabled:  true,
			},
			{
				ID:       "file.events_burst",
				Title:    "File change burst",
				Severity: miniedr.SeverityLow,
				Metric:   "file.events",
				Op:       ">=",
				Value:    50,
				Message:  "{value} file events detected (limit {threshold})",
				Source:   "FILECHANGE",
				Enabled:  true,
			},
			{
				ID:       "persist.change",
				Title:    "Persistence changes",
				Severity: miniedr.SeverityLow,
				Metric:   "persist.total_changes",
				Op:       ">=",
				Value:    1,
				Message:  "Persistence entries changed (total {value})",
				Source:   "PERSIST",
				Enabled:  true,
			},
		},
	}
}

func normalizeRulesConfig(cfg RulesConfig) RulesConfig {
	out := RulesConfig{Rules: make([]RuleDefinition, 0, len(cfg.Rules))}
	for i, rule := range cfg.Rules {
		rule.ID = strings.TrimSpace(rule.ID)
		rule.Title = strings.TrimSpace(rule.Title)
		rule.Metric = strings.TrimSpace(rule.Metric)
		rule.Op = normalizeOp(rule.Op)
		rule.Message = strings.TrimSpace(rule.Message)
		rule.Source = strings.TrimSpace(rule.Source)
		rule.Severity = normalizeSeverity(rule.Severity)
		if rule.ID == "" {
			rule.ID = fmt.Sprintf("custom.rule.%d", i+1)
		}
		if rule.Title == "" {
			if rule.ID != "" {
				rule.Title = rule.ID
			} else if rule.Metric != "" {
				rule.Title = rule.Metric
			}
		}
		if rule.Metric == "" {
			continue
		}
		out.Rules = append(out.Rules, rule)
	}
	return out
}

func (d *DashboardServer) rebuildDetector() {
	d.mu.Lock()
	d.rebuildDetectorLocked()
	d.mu.Unlock()
}

func (d *DashboardServer) rebuildDetectorLocked() {
	cfg := normalizeRulesConfig(d.rulesConfig)
	d.rulesConfig = cfg
	specs := make([]miniedr.RuleSpec, 0, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		if !rule.Enabled {
			continue
		}
		specs = append(specs, buildMetricRule(rule))
	}
	if !d.iocConfig.IsEmpty() {
		specs = append(specs, miniedr.RuleIOCMatch(d.iocConfig))
	}
	d.detector = &miniedr.Detector{
		Rules: specs,
		// Keep alert history per snapshot without dedup/rate-limit for the dashboard.
	}
}

func (d *DashboardServer) loadRulesConfig() RulesConfig {
	cfg := defaultRulesConfig()
	if d.rulesPath == "" {
		return cfg
	}
	data, err := os.ReadFile(d.rulesPath)
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaultRulesConfig()
	}
	return normalizeRulesConfig(cfg)
}

func (d *DashboardServer) saveRulesConfig(cfg RulesConfig) error {
	if d.rulesPath == "" {
		return nil
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(d.rulesPath, append(data, '\n'), 0o644)
}

func (d *DashboardServer) loadCustomMetricsConfig() []CustomMetric {
	if d.metricsPath == "" {
		return []CustomMetric{}
	}
	data, err := os.ReadFile(d.metricsPath)
	if err != nil {
		return []CustomMetric{}
	}
	var cfg metricsConfig
	if err := json.Unmarshal(data, &cfg); err == nil && len(cfg.Custom) > 0 {
		return normalizeCustomMetricsUnsafe(cfg.Custom)
	}
	var legacy []string
	if err := json.Unmarshal(data, &legacy); err == nil {
		return []CustomMetric{}
	}
	return []CustomMetric{}
}

func (d *DashboardServer) saveCustomMetricsConfig(metrics []CustomMetric) error {
	if d.metricsPath == "" {
		return nil
	}
	cfg := metricsConfig{Custom: metrics}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(d.metricsPath, append(data, '\n'), 0o644)
}

type metricsConfig struct {
	Custom []CustomMetric `json:"custom"`
}

func mergeMetrics(a, b []string) []string {
	merged := make([]string, 0, len(a)+len(b))
	seen := make(map[string]struct{})
	for _, m := range a {
		if m == "" {
			continue
		}
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		merged = append(merged, m)
	}
	for _, m := range b {
		if m == "" {
			continue
		}
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		merged = append(merged, m)
	}
	sort.Slice(merged, func(i, j int) bool {
		return metricLess(merged[i], merged[j])
	})
	return merged
}

func deriveDefaultMetrics(items []dashboardItem) []string {
	keys := make([]string, 0, 128)
	seen := make(map[string]struct{})
	add := func(k string) {
		if k == "" {
			return
		}
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	for _, item := range items {
		for k := range item.Info.Metrics {
			add(k)
		}
	}
	var hasCPU bool
	var hasNET bool
	for _, item := range items {
		switch strings.ToUpper(item.Name) {
		case "CPU":
			hasCPU = true
		case "NET":
			hasNET = true
		}
	}
	if hasCPU {
		add("cpu.total_pct")
		for i := 0; i < runtime.NumCPU(); i++ {
			add(fmt.Sprintf("cpu.core%d_pct", i))
		}
	}
	if hasNET {
		add("net.rx_bytes_per_sec")
		add("net.tx_bytes_per_sec")
		add("net.total_bytes_per_sec")
	}
	if hasAny(seen, "net.rx_bytes_per_sec", "net.tx_bytes_per_sec") {
		add("net.total_bytes_per_sec")
	}
	if hasAny(seen, "persist.added", "persist.changed", "persist.removed") {
		add("persist.total_changes")
	}
	sort.Slice(keys, func(i, j int) bool {
		return metricLess(keys[i], keys[j])
	})
	return keys
}

func customMetricNames(list []CustomMetric) []string {
	names := make([]string, 0, len(list))
	for _, cm := range list {
		if cm.Name == "" {
			continue
		}
		names = append(names, cm.Name)
	}
	return names
}

func toCustomMetricPayload(list []CustomMetric) []CustomMetricPayload {
	out := make([]CustomMetricPayload, 0, len(list))
	for _, cm := range list {
		if cm.Name == "" {
			continue
		}
		out = append(out, CustomMetricPayload{
			Name: cm.Name,
			Expr: formatMetricExpr(cm.Expr),
		})
	}
	return out
}

func normalizeCustomMetrics(input []CustomMetricPayload) ([]CustomMetric, error) {
	out := make([]CustomMetric, 0, len(input))
	seen := make(map[string]struct{})
	for _, raw := range input {
		name := strings.TrimSpace(raw.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		exprText := strings.TrimSpace(raw.Expr)
		if exprText == "" {
			return nil, fmt.Errorf("metric %q missing expr", name)
		}
		expr, err := parseMetricExpr(exprText)
		if err != nil {
			return nil, fmt.Errorf("metric %q: %w", name, err)
		}
		out = append(out, CustomMetric{Name: name, Expr: expr})
	}
	sort.Slice(out, func(i, j int) bool {
		return metricLess(out[i].Name, out[j].Name)
	})
	return out, nil
}

func normalizeCustomMetricsUnsafe(input []CustomMetric) []CustomMetric {
	out := make([]CustomMetric, 0, len(input))
	seen := make(map[string]struct{})
	for _, raw := range input {
		name := strings.TrimSpace(raw.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		if raw.Expr.Op == "" && raw.Expr.Metric == "" && raw.Expr.Value == nil {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, CustomMetric{Name: name, Expr: raw.Expr})
	}
	sort.Slice(out, func(i, j int) bool {
		return metricLess(out[i].Name, out[j].Name)
	})
	return out
}

func metricLess(a, b string) bool {
	ap, ai, aok := parseCoreMetric(a)
	bp, bi, bok := parseCoreMetric(b)
	if aok && bok && ap == bp {
		return ai < bi
	}
	return a < b
}

func parseCoreMetric(s string) (string, int, bool) {
	if !strings.HasPrefix(s, "cpu.core") || !strings.HasSuffix(s, "_pct") {
		return "", 0, false
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(s, "cpu.core"), "_pct")
	if mid == "" {
		return "", 0, false
	}
	n, err := strconv.Atoi(mid)
	if err != nil {
		return "", 0, false
	}
	return "cpu.core", n, true
}

type metricTokenType int

const (
	tokenEOF metricTokenType = iota
	tokenNumber
	tokenIdent
	tokenPlus
	tokenMinus
	tokenMul
	tokenDiv
	tokenLParen
	tokenRParen
)

type metricToken struct {
	typ metricTokenType
	val string
}

type metricLexer struct {
	input string
	pos   int
}

func newMetricLexer(input string) *metricLexer {
	return &metricLexer{input: input}
}

func (l *metricLexer) nextToken() metricToken {
	for l.pos < len(l.input) && l.input[l.pos] == ' ' {
		l.pos++
	}
	if l.pos >= len(l.input) {
		return metricToken{typ: tokenEOF}
	}
	ch := l.input[l.pos]
	switch ch {
	case '+':
		l.pos++
		return metricToken{typ: tokenPlus, val: "+"}
	case '-':
		l.pos++
		return metricToken{typ: tokenMinus, val: "-"}
	case '*':
		l.pos++
		return metricToken{typ: tokenMul, val: "*"}
	case '/':
		l.pos++
		return metricToken{typ: tokenDiv, val: "/"}
	case '(':
		l.pos++
		return metricToken{typ: tokenLParen, val: "("}
	case ')':
		l.pos++
		return metricToken{typ: tokenRParen, val: ")"}
	}
	if isDigit(ch) || ch == '.' {
		start := l.pos
		l.pos++
		for l.pos < len(l.input) && (isDigit(l.input[l.pos]) || l.input[l.pos] == '.') {
			l.pos++
		}
		return metricToken{typ: tokenNumber, val: l.input[start:l.pos]}
	}
	if isIdentStart(ch) {
		start := l.pos
		l.pos++
		for l.pos < len(l.input) && isIdentPart(l.input[l.pos]) {
			l.pos++
		}
		return metricToken{typ: tokenIdent, val: l.input[start:l.pos]}
	}
	l.pos++
	return metricToken{typ: tokenEOF}
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func isIdentStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_' || b == '.'
}

func isIdentPart(b byte) bool {
	return isIdentStart(b) || isDigit(b)
}

type metricParser struct {
	lexer *metricLexer
	cur   metricToken
}

func newMetricParser(input string) *metricParser {
	lex := newMetricLexer(input)
	return &metricParser{lexer: lex, cur: lex.nextToken()}
}

func (p *metricParser) next() {
	p.cur = p.lexer.nextToken()
}

func parseMetricExpr(input string) (MetricExpr, error) {
	p := newMetricParser(input)
	expr, err := p.parseExpr()
	if err != nil {
		return MetricExpr{}, err
	}
	if p.cur.typ != tokenEOF {
		return MetricExpr{}, fmt.Errorf("unexpected token %q", p.cur.val)
	}
	return expr, nil
}

func (p *metricParser) parseExpr() (MetricExpr, error) {
	left, err := p.parseTerm()
	if err != nil {
		return MetricExpr{}, err
	}
	for p.cur.typ == tokenPlus || p.cur.typ == tokenMinus {
		op := p.cur.val
		p.next()
		right, err := p.parseTerm()
		if err != nil {
			return MetricExpr{}, err
		}
		left = MetricExpr{Op: op, Args: []MetricExpr{left, right}}
	}
	return left, nil
}

func (p *metricParser) parseTerm() (MetricExpr, error) {
	left, err := p.parseFactor()
	if err != nil {
		return MetricExpr{}, err
	}
	for p.cur.typ == tokenMul || p.cur.typ == tokenDiv {
		op := p.cur.val
		p.next()
		right, err := p.parseFactor()
		if err != nil {
			return MetricExpr{}, err
		}
		left = MetricExpr{Op: op, Args: []MetricExpr{left, right}}
	}
	return left, nil
}

func (p *metricParser) parseFactor() (MetricExpr, error) {
	switch p.cur.typ {
	case tokenNumber:
		val, err := strconv.ParseFloat(p.cur.val, 64)
		if err != nil {
			return MetricExpr{}, fmt.Errorf("invalid number %q", p.cur.val)
		}
		p.next()
		return MetricExpr{Value: &val}, nil
	case tokenIdent:
		ident := p.cur.val
		p.next()
		return MetricExpr{Metric: ident}, nil
	case tokenMinus:
		p.next()
		factor, err := p.parseFactor()
		if err != nil {
			return MetricExpr{}, err
		}
		zero := 0.0
		return MetricExpr{Op: "-", Args: []MetricExpr{{Value: &zero}, factor}}, nil
	case tokenLParen:
		p.next()
		expr, err := p.parseExpr()
		if err != nil {
			return MetricExpr{}, err
		}
		if p.cur.typ != tokenRParen {
			return MetricExpr{}, fmt.Errorf("expected )")
		}
		p.next()
		return expr, nil
	default:
		return MetricExpr{}, fmt.Errorf("unexpected token %q", p.cur.val)
	}
}

func formatMetricExpr(expr MetricExpr) string {
	return formatMetricExprWithPrec(expr, 0)
}

func formatMetricExprWithPrec(expr MetricExpr, parentPrec int) string {
	if expr.Metric != "" {
		return expr.Metric
	}
	if expr.Value != nil {
		return formatFloat(*expr.Value)
	}
	if expr.Op == "" || len(expr.Args) == 0 {
		return ""
	}
	prec := opPrecedence(expr.Op)
	parts := make([]string, 0, len(expr.Args))
	for _, arg := range expr.Args {
		parts = append(parts, formatMetricExprWithPrec(arg, prec))
	}
	out := strings.Join(parts, " "+expr.Op+" ")
	if prec < parentPrec {
		return "(" + out + ")"
	}
	return out
}

func opPrecedence(op string) int {
	switch op {
	case "*", "/":
		return 2
	case "+", "-":
		return 1
	default:
		return 0
	}
}

func hasAny(seen map[string]struct{}, keys ...string) bool {
	for _, k := range keys {
		if _, ok := seen[k]; ok {
			return true
		}
	}
	return false
}

func buildMetricRule(def RuleDefinition) miniedr.RuleSpec {
	return miniedr.RuleSpec{
		ID:       def.ID,
		Title:    def.Title,
		Severity: def.Severity,
		Source:   def.Source,
		Eval: func(info capturer.InfoData) []miniedr.Alert {
			if def.Source != "" && !strings.EqualFold(def.Source, info.Meta.Capturer) {
				return nil
			}
			val, ok := metricValue(info, def.Metric)
			if !ok {
				return nil
			}
			if !compareMetric(val, def.Op, def.Value) {
				return nil
			}
			title := renderTemplate(def.Title, def.Metric, val, def.Value)
			message := renderTemplate(def.Message, def.Metric, val, def.Value)
			if message == "" {
				message = fmt.Sprintf("%s=%s %s %s", def.Metric, formatFloat(val), def.Op, formatFloat(def.Value))
			}
			return []miniedr.Alert{{
				RuleID:   def.ID,
				Title:    title,
				Severity: def.Severity,
				Message:  message,
				Evidence: map[string]any{def.Metric: val},
			}}
		},
	}
}

func metricValue(info capturer.InfoData, key string) (float64, bool) {
	if info.Metrics != nil {
		if v, ok := info.Metrics[key]; ok {
			return v, true
		}
	}
	switch key {
	case "net.total_bytes_per_sec":
		var total float64
		rx, okRx := metricValue(info, "net.rx_bytes_per_sec")
		tx, okTx := metricValue(info, "net.tx_bytes_per_sec")
		if !okRx && !okTx {
			return 0, false
		}
		total = rx + tx
		return total, true
	case "persist.total_changes":
		added, okAdded := metricValue(info, "persist.added")
		changed, okChanged := metricValue(info, "persist.changed")
		removed, okRemoved := metricValue(info, "persist.removed")
		if !okAdded && !okChanged && !okRemoved {
			return 0, false
		}
		return added + changed + removed, true
	}
	return 0, false
}

func compareMetric(value float64, op string, threshold float64) bool {
	switch op {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	case "==":
		return value == threshold
	case "!=":
		return value != threshold
	default:
		return value >= threshold
	}
}

func renderTemplate(text, metric string, value, threshold float64) string {
	if text == "" {
		return ""
	}
	out := strings.ReplaceAll(text, "{metric}", metric)
	out = strings.ReplaceAll(out, "{value}", formatFloat(value))
	out = strings.ReplaceAll(out, "{threshold}", formatFloat(threshold))
	return out
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func normalizeOp(op string) string {
	op = strings.TrimSpace(op)
	switch op {
	case ">", ">=", "<", "<=", "==", "!=":
		return op
	default:
		return ">="
	}
}

func normalizeSeverity(s miniedr.AlertSeverity) miniedr.AlertSeverity {
	switch miniedr.AlertSeverity(strings.ToLower(string(s))) {
	case miniedr.SeverityInfo, miniedr.SeverityLow, miniedr.SeverityMedium, miniedr.SeverityHigh, miniedr.SeverityCritical:
		return miniedr.AlertSeverity(strings.ToLower(string(s)))
	default:
		return miniedr.SeverityInfo
	}
}

func applyCustomMetrics(info *capturer.InfoData, customs []CustomMetric) {
	if info == nil || len(customs) == 0 {
		return
	}
	if info.Metrics == nil {
		info.Metrics = make(map[string]float64)
	}
	for _, cm := range customs {
		if cm.Name == "" {
			continue
		}
		if _, exists := info.Metrics[cm.Name]; exists {
			continue
		}
		val, ok := evalMetricExpr(cm.Expr, info.Metrics)
		if !ok {
			continue
		}
		info.Metrics[cm.Name] = val
	}
}

func evalMetricExpr(expr MetricExpr, metrics map[string]float64) (float64, bool) {
	if expr.Metric != "" {
		val, ok := metrics[expr.Metric]
		return val, ok
	}
	if expr.Value != nil {
		return *expr.Value, true
	}
	if expr.Op == "" || len(expr.Args) == 0 {
		return 0, false
	}
	switch expr.Op {
	case "+":
		total := 0.0
		for _, arg := range expr.Args {
			val, ok := evalMetricExpr(arg, metrics)
			if !ok {
				return 0, false
			}
			total += val
		}
		return total, true
	case "-":
		val, ok := evalMetricExpr(expr.Args[0], metrics)
		if !ok {
			return 0, false
		}
		if len(expr.Args) == 1 {
			return -val, true
		}
		for i := 1; i < len(expr.Args); i++ {
			next, ok := evalMetricExpr(expr.Args[i], metrics)
			if !ok {
				return 0, false
			}
			val -= next
		}
		return val, true
	case "*":
		total := 1.0
		for _, arg := range expr.Args {
			val, ok := evalMetricExpr(arg, metrics)
			if !ok {
				return 0, false
			}
			total *= val
		}
		return total, true
	case "/":
		val, ok := evalMetricExpr(expr.Args[0], metrics)
		if !ok {
			return 0, false
		}
		for i := 1; i < len(expr.Args); i++ {
			next, ok := evalMetricExpr(expr.Args[i], metrics)
			if !ok || next == 0 {
				return 0, false
			}
			val /= next
		}
		return val, true
	default:
		return 0, false
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
