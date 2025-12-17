package miniedr_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jaewooli/miniedr"
)

type dashStub struct {
	name    string
	info    string
	verbose string
	err     error
}

type changingStub struct {
	name  string
	infos []string
	idx   int
}

func (c *changingStub) Capture() error {
	if c.idx < 0 {
		c.idx = 0
	} else if c.idx < len(c.infos)-1 {
		c.idx++
	}
	return nil
}
func (c *changingStub) GetInfo() (string, error) {
	if c.idx < 0 {
		return "", nil
	}
	return c.infos[c.idx], nil
}
func (c *changingStub) GetVerboseInfo() (string, error) { return "", nil }

func (d *dashStub) Capture() error { return d.err }
func (d *dashStub) GetInfo() (string, error) {
	if d.err != nil {
		return "", d.err
	}
	return d.info, nil
}
func (d *dashStub) GetVerboseInfo() (string, error) {
	if d.err != nil {
		return "", d.err
	}
	return d.verbose, nil
}

func TestDashboardSnapshotAndRender(t *testing.T) {
	cs := miniedr.Capturers{
		&dashStub{name: "cpu", info: "cpu info", verbose: "cpu verbose"},
		&dashStub{name: "mem", info: "mem info", verbose: "mem verbose"},
	}
	ds := miniedr.NewDashboardServer(cs, "TestDash", true)

	now := time.Unix(100, 0)
	ds.SetNowFunc(func() time.Time { return now })
	ds.CaptureNow()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	ds.ServeHTTP(w, req)

	res := w.Result()
	body := readBody(t, res)

	assertContains(t, body, "TestDash")
	assertContains(t, body, "cpu info")
	assertContains(t, body, "cpu verbose")
	assertContains(t, body, "mem info")
	assertContains(t, body, "mem verbose")
	assertContains(t, body, now.Format("2006-01-02T15:04:05"))
}

func TestDashboardChangedFlag(t *testing.T) {
	changing := &changingStub{name: "cpu", infos: []string{"cpu info v1", "cpu info v2"}, idx: -1}
	ds := miniedr.NewDashboardServer(miniedr.Capturers{changing}, "TestDash", false)
	ds.SetNowFunc(func() time.Time { return time.Unix(100, 0) })

	ds.CaptureNow()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w1 := httptest.NewRecorder()
	ds.ServeHTTP(w1, req)
	body1 := readBody(t, w1.Result())
	assertContains(t, body1, "cpu info v1")
	if strings.Contains(body1, "pill changed") {
		t.Fatalf("did not expect changed pill on first render")
	}

	ds.CaptureNow()
	w2 := httptest.NewRecorder()
	ds.ServeHTTP(w2, req)
	body2 := readBody(t, w2.Result())
	assertContains(t, body2, "cpu info v2")
	assertContains(t, body2, "pill changed")
}

func TestDashboardChangedIgnoresTimestamps(t *testing.T) {
	changing := &changingStub{name: "cpu", infos: []string{
		"CPUSnapshot(at=1970-01-01T00:00:10Z, totalUsage=10%)",
		"CPUSnapshot(at=1970-01-01T00:00:20Z, totalUsage=10%)",
	}}
	ds := miniedr.NewDashboardServer(miniedr.Capturers{changing}, "TestDash", false)
	ds.SetNowFunc(func() time.Time { return time.Unix(100, 0) })

	ds.CaptureNow()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w1 := httptest.NewRecorder()
	ds.ServeHTTP(w1, req)
	body1 := readBody(t, w1.Result())
	assertContains(t, body1, "totalUsage=10%")
	if strings.Contains(body1, "pill changed") {
		t.Fatalf("did not expect changed pill on first render")
	}

	ds.CaptureNow()
	w2 := httptest.NewRecorder()
	ds.ServeHTTP(w2, req)
	body2 := readBody(t, w2.Result())
	assertContains(t, body2, "totalUsage=10%")
	if strings.Contains(body2, "pill changed") {
		t.Fatalf("timestamp-only differences should not mark changed")
	}
}

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}

func TestDashboardEventStream(t *testing.T) {
	ds := miniedr.NewDashboardServer(miniedr.Capturers{
		&dashStub{name: "cpu", info: "cpu info"},
	}, "TestDash", false)
	ds.SetNowFunc(func() time.Time { return time.Unix(200, 0) })
	ds.SetAutoRefresh(false, 10)
	ds.SetEventRefresh(true)

	mux := http.NewServeMux()
	mux.Handle("/", ds)
	mux.HandleFunc("/events", ds.ServeEvents)
	server := httptest.NewServer(mux)
	defer server.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("event stream request: %v", err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	gotCh := make(chan string, 1)

	// reader loop
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				close(gotCh)
				return
			}
			gotCh <- line
		}
	}()

	// Wait to ensure no immediate non-empty event
	select {
	case line := <-gotCh:
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, ":") {
			t.Fatalf("expected no immediate event, got %q", line)
		}
	case <-time.After(200 * time.Millisecond):
	}

	// Trigger capture -> should get event
	go func() {
		ds.CaptureNow()
	}()

	timeout := time.After(2 * time.Second)
	for {
		select {
		case line := <-gotCh:
			trim := strings.TrimSpace(line)
			if trim == "" || strings.HasPrefix(trim, ":") {
				continue
			}
			if !strings.HasPrefix(trim, "data:") {
				t.Fatalf("expected data line, got %q", line)
			}
			return
		case <-timeout:
			t.Fatalf("expected event after capture")
		}
	}
}
