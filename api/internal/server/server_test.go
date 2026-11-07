package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moabdelazem/pulsecheck-api/internal/store"
)

func TestValidateSettings(t *testing.T) {
	ok := store.DefaultSettings()
	ok.URL = "https://example.com"
	if err := validateSettings(ok); err != nil {
		t.Fatalf("defaults should be valid: %v", err)
	}
	status := func(n int) *int { return &n }
	bad := map[string]func(*store.Settings){
		"long name":      func(s *store.Settings) { s.Name = strings.Repeat("é", 101) },
		"POST method":    func(s *store.Settings) { s.Method = "POST" },
		"interval low":   func(s *store.Settings) { s.IntervalSeconds = 9 },
		"interval high":  func(s *store.Settings) { s.IntervalSeconds = 86401 },
		"timeout zero":   func(s *store.Settings) { s.TimeoutSeconds = 0 },
		"timeout high":   func(s *store.Settings) { s.TimeoutSeconds = 31 },
		"status too low": func(s *store.Settings) { s.ExpectedStatus = status(99) },
		"status too big": func(s *store.Settings) { s.ExpectedStatus = status(600) },
	}
	for name, mutate := range bad {
		s := ok
		mutate(&s)
		if validateSettings(s) == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestValidateURL(t *testing.T) {
	good := []string{"https://example.com", "http://localhost:8080/health"}
	bad := []string{"", "example.com", "ftp://example.com", "https://", "/path", strings.Repeat("a", 2049)}
	for _, u := range good {
		if err := validateURL(u); err != nil {
			t.Errorf("validateURL(%q) = %v, want nil", u, err)
		}
	}
	for _, u := range bad {
		if validateURL(u) == nil {
			t.Errorf("validateURL(%q) = nil, want error", u)
		}
	}
}

// TestAPI runs the full monitor lifecycle against a real database.
// It needs DATABASE_URL and the schema from scripts/dev/schema.sql (`make test` sets it).
func TestAPI(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close) // registered first, so it runs after the cleanups below

	srv := httptest.NewServer(New(db))
	defer srv.Close()

	// Unique URLs so the test never collides with real data.
	url1 := fmt.Sprintf("https://test-%d.example.com", time.Now().UnixNano())
	url2 := url1 + "/v2"
	t.Cleanup(func() {
		db.Exec(context.Background(), `DELETE FROM monitors WHERE url IN ($1, $2)`, url1, url2)
	})

	call := func(method, path, body string, wantStatus int, out any) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != wantStatus {
			var e map[string]any
			json.NewDecoder(resp.Body).Decode(&e)
			t.Fatalf("%s %s: status %d, want %d (body %v)", method, path, resp.StatusCode, wantStatus, e)
		}
		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				t.Fatalf("%s %s: decode: %v", method, path, err)
			}
		}
	}

	type monitor struct {
		ID              int64  `json:"id"`
		URL             string `json:"url"`
		Name            string `json:"name"`
		Method          string `json:"method"`
		IntervalSeconds int    `json:"interval_seconds"`
		TimeoutSeconds  int    `json:"timeout_seconds"`
		ExpectedStatus  *int   `json:"expected_status"`
		Paused          bool   `json:"paused"`
	}

	call("GET", "/healthz", "", 200, nil)
	call("GET", "/livez", "", 200, nil)

	// Create, plus the ways create can fail.
	var m monitor
	call("POST", "/monitors", `{"url":"`+url1+`"}`, 201, &m)
	if m.ID == 0 || m.URL != url1 || m.Method != "GET" || m.IntervalSeconds != 30 || m.TimeoutSeconds != 5 || m.ExpectedStatus != nil || m.Paused {
		t.Fatalf("created with defaults: %+v", m)
	}
	call("POST", "/monitors", `{"url":"`+url2+`","interval_seconds":5}`, 400, nil)
	call("POST", "/monitors", `{"url":"`+url1+`"}`, 409, nil)
	call("POST", "/monitors", `{"url":"not a url"}`, 400, nil)
	call("POST", "/monitors", `{"url":"`+url2+`","extra":1}`, 400, nil)
	call("POST", "/monitors", `not json`, 400, nil)

	// Read.
	id := fmt.Sprint(m.ID)
	var got monitor
	call("GET", "/monitors/"+id, "", 200, &got)
	if got.ID != m.ID || got.URL != m.URL {
		t.Fatalf("get = %+v, want %+v", got, m)
	}
	var list []monitor
	call("GET", "/monitors", "", 200, &list)
	found := false
	for _, x := range list {
		found = found || x.ID == m.ID
	}
	if !found {
		t.Fatalf("monitor %+v missing from list", m)
	}
	call("GET", "/monitors/abc", "", 400, nil)
	call("GET", "/monitors/999999999999", "", 404, nil)

	// Update: PATCH changes only the fields sent.
	call("PATCH", "/monitors/"+id, `{"url":"`+url2+`","name":"  Example  ","method":"head","interval_seconds":60,"expected_status":204}`, 200, &got)
	if got.URL != url2 || got.Name != "Example" || got.Method != "HEAD" || got.IntervalSeconds != 60 ||
		got.ExpectedStatus == nil || *got.ExpectedStatus != 204 || got.TimeoutSeconds != 5 {
		t.Fatalf("after patch: %+v", got)
	}
	call("PATCH", "/monitors/"+id, `{"paused":true}`, 200, &got)
	if !got.Paused || got.Name != "Example" || got.IntervalSeconds != 60 {
		t.Fatalf("pause changed other fields: %+v", got)
	}
	call("PATCH", "/monitors/"+id, `{"expected_status":null,"paused":false}`, 200, &got)
	if got.ExpectedStatus != nil || got.Paused {
		t.Fatalf("clear expected_status / resume: %+v", got)
	}
	call("PATCH", "/monitors/"+id, `{"timeout_seconds":99}`, 400, nil)
	call("PATCH", "/monitors/999999999999", `{"paused":true}`, 404, nil)

	// Results and stats: empty first, then with two recorded checks.
	var results []map[string]any
	call("GET", "/monitors/"+id+"/results", "", 200, &results)
	if results == nil || len(results) != 0 {
		t.Fatalf("want empty JSON array, got %v", results)
	}
	_, err = db.Exec(context.Background(), `
		INSERT INTO check_results (monitor_id, up, status_code, latency_ms, error, checked_at) VALUES
		($1, true, 200, 100, NULL, now() - interval '1 minute'),
		($1, false, NULL, 300, 'timeout', now())`, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	call("GET", "/monitors/"+id+"/results?limit=1", "", 200, &results)
	if len(results) != 1 || results[0]["up"] != false {
		t.Fatalf("want newest (down) result only, got %v", results)
	}
	call("GET", "/monitors/"+id+"/results?limit=0", "", 400, nil)

	var stats struct {
		Stats struct {
			Checks        int64   `json:"checks"`
			UptimePercent float64 `json:"uptime_percent"`
			AvgLatencyMS  float64 `json:"avg_latency_ms"`
		} `json:"stats"`
	}
	call("GET", "/monitors/"+id+"/stats?window=1h", "", 200, &stats)
	if stats.Stats.Checks != 2 || stats.Stats.UptimePercent != 50 || stats.Stats.AvgLatencyMS != 200 {
		t.Fatalf("stats = %+v, want 2 checks, 50%% uptime, 200ms avg", stats.Stats)
	}
	call("GET", "/monitors/"+id+"/stats?window=nope", "", 400, nil)

	// Latency buckets: both checks fall in one 1h bucket; only the up check counts for latency.
	var lat struct {
		Buckets []struct {
			Checks       int64    `json:"checks"`
			Down         int64    `json:"down"`
			AvgLatencyMS *float64 `json:"avg_latency_ms"`
		} `json:"buckets"`
	}
	call("GET", "/monitors/"+id+"/latency?window=2h&bucket=1m", "", 200, &lat)
	var checks, down int64
	for _, b := range lat.Buckets {
		checks += b.Checks
		down += b.Down
		if b.Down == b.Checks && b.AvgLatencyMS != nil {
			t.Errorf("all-down bucket should have null latency: %+v", b)
		}
	}
	if checks != 2 || down != 1 {
		t.Fatalf("latency buckets: %d checks, %d down, want 2 and 1 (%+v)", checks, down, lat.Buckets)
	}
	call("GET", "/monitors/"+id+"/latency?window=720h&bucket=10s", "", 400, nil)
	call("GET", "/monitors/"+id+"/latency?bucket=1s", "", 400, nil)

	// Delete.
	call("DELETE", "/monitors/"+id, "", 204, nil)
	call("GET", "/monitors/"+id, "", 404, nil)
	call("DELETE", "/monitors/"+id, "", 404, nil)
	call("GET", "/monitors/"+id+"/results", "", 404, nil)

	// Metrics: labelled by route pattern, not by the raw path with the id in it.
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{
		`http_requests_total{code="201",route="POST /monitors"}`,
		`http_requests_total{code="404",route="GET /monitors/{id}"}`,
		`http_request_duration_seconds_bucket{route="GET /healthz"`,
		"go_goroutines",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics missing %q", want)
		}
	}
	if strings.Contains(string(body), "/monitors/"+id) {
		t.Error("metrics contain a raw monitor id path")
	}
}
