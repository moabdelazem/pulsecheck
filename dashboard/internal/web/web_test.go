package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moabdelazem/pulsecheck-dashboard/internal/apiclient"
)

// fakeAPI imitates the Pulse Check API: monitor 1 has a name and two results,
// monitor 2 is paused and never checked. PATCH bodies are recorded in patches.
type fakeAPI struct {
	*httptest.Server
	mu      sync.Mutex
	patches []map[string]any
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{}
	now := time.Now()
	send := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	m1 := map[string]any{"id": 1, "url": "https://up.example.com", "name": "Main site", "method": "GET",
		"interval_seconds": 60, "timeout_seconds": 5, "expected_status": nil, "paused": false, "created_at": now}
	m2 := map[string]any{"id": 2, "url": "https://paused.example.com", "name": "", "method": "HEAD",
		"interval_seconds": 300, "timeout_seconds": 5, "expected_status": 204, "paused": true, "created_at": now}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /monitors", func(w http.ResponseWriter, r *http.Request) { send(w, 200, []any{m1, m2}) })
	mux.HandleFunc("GET /monitors/{id}", func(w http.ResponseWriter, r *http.Request) {
		send(w, 404, map[string]string{"error": "monitor not found"})
	})
	mux.HandleFunc("GET /monitors/1", func(w http.ResponseWriter, r *http.Request) { send(w, 200, m1) })
	mux.HandleFunc("GET /monitors/2", func(w http.ResponseWriter, r *http.Request) { send(w, 200, m2) })
	mux.HandleFunc("GET /monitors/{id}/results", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "1" {
			send(w, 200, []any{})
			return
		}
		results := []map[string]any{
			{"id": 2, "up": true, "status_code": 200, "latency_ms": 120, "checked_at": now},
			{"id": 1, "up": false, "status_code": nil, "latency_ms": 5000, "error": "timeout", "checked_at": now.Add(-time.Minute)},
		}
		if r.URL.Query().Get("limit") == "1" {
			results = results[:1]
		}
		send(w, 200, results)
	})
	mux.HandleFunc("GET /monitors/{id}/stats", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, map[string]any{"stats": map[string]any{"checks": 200, "up_checks": 199, "uptime_percent": 99.5, "avg_latency_ms": 123.4}})
	})
	mux.HandleFunc("GET /monitors/{id}/latency", func(w http.ResponseWriter, r *http.Request) {
		window, _ := time.ParseDuration(r.URL.Query().Get("window"))
		bucket, _ := time.ParseDuration(r.URL.Query().Get("bucket"))
		start := now.Add(-window).Truncate(bucket)
		send(w, 200, map[string]any{"buckets": []map[string]any{
			{"time": start.Add(bucket), "checks": 4, "down": 0, "avg_latency_ms": 100.0},
			{"time": start.Add(2 * bucket), "checks": 4, "down": 1, "avg_latency_ms": 180.0},
			{"time": start.Add(3 * bucket), "checks": 4, "down": 4, "avg_latency_ms": nil},
		}})
	})
	mux.HandleFunc("POST /monitors", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["url"] == "https://dup.example.com" {
			send(w, 409, map[string]string{"error": "a monitor with this url already exists"})
			return
		}
		send(w, 201, m1)
	})
	mux.HandleFunc("PATCH /monitors/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.patches = append(f.patches, body)
		f.mu.Unlock()
		if n, ok := body["timeout_seconds"].(float64); ok && n > 30 {
			send(w, 400, map[string]string{"error": "timeout_seconds must be between 1 and 30"})
			return
		}
		send(w, 200, m1)
	})
	mux.HandleFunc("DELETE /monitors/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAPI) lastPatch() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.patches) == 0 {
		return nil
	}
	return f.patches[len(f.patches)-1]
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func postForm(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func mustContain(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("page is missing %q", w)
		}
	}
}

func TestOverview(t *testing.T) {
	h := New(apiclient.New(newFakeAPI(t).URL))
	rec := get(t, h, "/")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	mustContain(t, rec.Body.String(),
		"Main site", "https://up.example.com", `class="pill up"`, "99.5%", "123 ms", `href="/monitors/1"`,
		`class="pill paused"`, "https://paused.example.com", "5m", `action="/monitors/2/resume"`, `action="/monitors/1/pause"`)
}

func TestOverview_APIDown(t *testing.T) {
	api := httptest.NewServer(http.NotFoundHandler())
	api.Close() // nothing listening
	rec := get(t, New(apiclient.New(api.URL)), "/")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", rec.Code)
	}
	mustContain(t, rec.Body.String(), "The API is not responding")
}

func TestDetail(t *testing.T) {
	h := New(apiclient.New(newFakeAPI(t).URL))
	for _, rg := range []string{"", "1h", "7d", "bogus"} {
		rec := get(t, h, "/monitors/1?range="+rg)
		if rec.Code != 200 {
			t.Fatalf("range %q: status %d", rg, rec.Code)
		}
		mustContain(t, rec.Body.String(),
			"Main site", "timeout", "5000 ms", "99.50%", `class="card strip"`,
			`<svg class="chart"`, `class="line" d="M`, `class="down-band"`, "failed check", `aria-current="page"`,
			`name="interval_seconds"`, `value="60"`)
	}
	mustContain(t, get(t, h, "/monitors/2").Body.String(), `class="pill paused"`, "Resume checks", `value="204"`)

	if rec := get(t, h, "/monitors/99"); rec.Code != 404 {
		t.Errorf("unknown monitor: status %d, want 404", rec.Code)
	}
	if rec := get(t, h, "/monitors/abc"); rec.Code != 404 {
		t.Errorf("bad id: status %d, want 404", rec.Code)
	}
}

func TestCreate(t *testing.T) {
	h := New(apiclient.New(newFakeAPI(t).URL))

	rec := postForm(t, h, "/monitors", url.Values{"url": {"https://new.example.com"}, "name": {"New"}, "interval_seconds": {"300"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("create: %d to %q, want 303 to /", rec.Code, rec.Header().Get("Location"))
	}

	// The API's error message is passed back and shown on the overview page.
	rec = postForm(t, h, "/monitors", url.Values{"url": {"https://dup.example.com"}})
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Fatalf("duplicate: redirect %q has no error", loc)
	}
	mustContain(t, get(t, h, loc).Body.String(), "a monitor with this url already exists")
}

func TestSettings(t *testing.T) {
	api := newFakeAPI(t)
	h := New(apiclient.New(api.URL))
	form := url.Values{"name": {" Renamed "}, "method": {"HEAD"}, "interval_seconds": {"120"}, "timeout_seconds": {"10"}, "expected_status": {""}}

	rec := postForm(t, h, "/monitors/1/settings", form)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/monitors/1" {
		t.Fatalf("save: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	p := api.lastPatch()
	if p["name"] != "Renamed" || p["method"] != "HEAD" || p["interval_seconds"] != 120.0 || p["timeout_seconds"] != 10.0 {
		t.Fatalf("patch sent: %v", p)
	}
	if v, ok := p["expected_status"]; !ok || v != nil {
		t.Fatalf("empty expected status should be sent as null, got %v (present %v)", v, ok)
	}

	form.Set("expected_status", "204")
	postForm(t, h, "/monitors/1/settings", form)
	if api.lastPatch()["expected_status"] != 204.0 {
		t.Fatalf("expected_status not sent: %v", api.lastPatch())
	}

	// Bad number: caught by the dashboard. Out of range: the API's message is shown.
	form.Set("interval_seconds", "soon")
	if loc := postForm(t, h, "/monitors/1/settings", form).Header().Get("Location"); !strings.HasPrefix(loc, "/monitors/1?error=") {
		t.Fatalf("bad interval: redirect %q", loc)
	}
	form.Set("interval_seconds", "60")
	form.Set("timeout_seconds", "99")
	loc := postForm(t, h, "/monitors/1/settings", form).Header().Get("Location")
	mustContain(t, get(t, h, loc).Body.String(), "timeout_seconds must be between 1 and 30")
}

func TestPauseResume(t *testing.T) {
	api := newFakeAPI(t)
	h := New(apiclient.New(api.URL))

	rec := postForm(t, h, "/monitors/1/pause", nil)
	if rec.Header().Get("Location") != "/monitors/1" || api.lastPatch()["paused"] != true {
		t.Fatalf("pause: redirect %q, patch %v", rec.Header().Get("Location"), api.lastPatch())
	}
	rec = postForm(t, h, "/monitors/1/resume", url.Values{"back": {"/"}})
	if rec.Header().Get("Location") != "/" || api.lastPatch()["paused"] != false {
		t.Fatalf("resume: redirect %q, patch %v", rec.Header().Get("Location"), api.lastPatch())
	}
	// Only "/" is accepted as a back target, so this cannot redirect off-site.
	rec = postForm(t, h, "/monitors/1/resume", url.Values{"back": {"https://evil.example"}})
	if rec.Header().Get("Location") != "/monitors/1" {
		t.Fatalf("open redirect: %q", rec.Header().Get("Location"))
	}
}

func TestDelete(t *testing.T) {
	h := New(apiclient.New(newFakeAPI(t).URL))
	rec := postForm(t, h, "/monitors/1/delete", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", rec.Code)
	}
}

func TestCrossSitePostRejected(t *testing.T) {
	h := New(apiclient.New(newFakeAPI(t).URL))
	req := httptest.NewRequest("POST", "/monitors/1/delete", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
}

func TestHealthAndMetrics(t *testing.T) {
	h := New(apiclient.New(newFakeAPI(t).URL))
	if rec := get(t, h, "/livez"); rec.Code != 200 {
		t.Fatalf("livez: %d", rec.Code)
	}
	get(t, h, "/monitors/1")
	body := get(t, h, "/metrics").Body.String()
	for _, want := range []string{`http_requests_total{code="200",route="GET /monitors/{id}"}`, "go_goroutines"} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}

func TestBuildChart(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	rg := findRange("1h")
	ms := func(v float64) *float64 { return &v }
	b := []apiclient.Bucket{
		{Time: now.Add(-60 * time.Minute), Checks: 2, AvgLatencyMS: ms(100)},
		{Time: now.Add(-59 * time.Minute), Checks: 2, AvgLatencyMS: ms(340)},
		// 10 minute gap: the line must restart instead of bridging it
		{Time: now.Add(-49 * time.Minute), Checks: 2, Down: 1, AvgLatencyMS: ms(200)},
		{Time: now.Add(-48 * time.Minute), Checks: 2, Down: 2},
	}
	c := buildChart(b, rg, now)

	if got := strings.Count(c.Path, "M"); got != 2 {
		t.Errorf("path %q: %d segments, want 2", c.Path, got)
	}
	if !strings.HasPrefix(c.Path, "M") || strings.Count(c.Path, "L") != 1 {
		t.Errorf("path %q: want M, L, then M for the point after the gap", c.Path)
	}
	if len(c.Slots) != 4 || !c.Slots[2].Down || !c.Slots[3].Down || c.Slots[0].Down {
		t.Errorf("slots: %+v", c.Slots)
	}
	if c.YTicks[2].Label != "500 ms" {
		t.Errorf("top tick %q, want 500 ms (340 rounded up)", c.YTicks[2].Label)
	}
	for _, s := range c.Slots {
		if s.X < c.Left || s.X+s.W > c.Right+0.01 {
			t.Errorf("slot outside plot area: %+v", s)
		}
	}
	if !strings.Contains(c.Summary, "2 time slots had failed checks") {
		t.Errorf("summary %q", c.Summary)
	}
	if !buildChart(nil, rg, now).Empty {
		t.Error("no buckets should give an empty chart")
	}
}

func TestNiceCeil(t *testing.T) {
	for in, want := range map[float64]float64{0: 100, 7: 10, 120: 200, 340: 500, 1000: 1000, 1001: 2000} {
		if got := niceCeil(in); got != want {
			t.Errorf("niceCeil(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestEvery(t *testing.T) {
	for in, want := range map[int]string{30: "30s", 90: "90s", 300: "5m", 3600: "1h", 86400: "24h"} {
		if got := every(in); got != want {
			t.Errorf("every(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestAgo(t *testing.T) {
	cases := map[time.Duration]string{
		5 * time.Second: "5s ago",
		3 * time.Minute: "3m ago",
		2 * time.Hour:   "2h ago",
		50 * time.Hour:  "2d ago",
	}
	for d, want := range cases {
		if got := ago(time.Now().Add(-d)); got != want {
			t.Errorf("ago(-%v) = %q, want %q", d, got, want)
		}
	}
}
