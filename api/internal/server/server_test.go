package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

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
	defer db.Close()

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
		ID  int64  `json:"id"`
		URL string `json:"url"`
	}

	call("GET", "/healthz", "", 200, nil)

	// Create, plus the ways create can fail.
	var m monitor
	call("POST", "/monitors", `{"url":"`+url1+`"}`, 201, &m)
	if m.ID == 0 || m.URL != url1 {
		t.Fatalf("created %+v", m)
	}
	call("POST", "/monitors", `{"url":"`+url1+`"}`, 409, nil)
	call("POST", "/monitors", `{"url":"not a url"}`, 400, nil)
	call("POST", "/monitors", `{"url":"`+url2+`","extra":1}`, 400, nil)
	call("POST", "/monitors", `not json`, 400, nil)

	// Read.
	id := fmt.Sprint(m.ID)
	var got monitor
	call("GET", "/monitors/"+id, "", 200, &got)
	if got != m {
		t.Fatalf("get = %+v, want %+v", got, m)
	}
	var list []monitor
	call("GET", "/monitors", "", 200, &list)
	found := false
	for _, x := range list {
		found = found || x == m
	}
	if !found {
		t.Fatalf("monitor %+v missing from list", m)
	}
	call("GET", "/monitors/abc", "", 400, nil)
	call("GET", "/monitors/999999999999", "", 404, nil)

	// Update.
	call("PUT", "/monitors/"+id, `{"url":"`+url2+`"}`, 200, &got)
	if got.URL != url2 {
		t.Fatalf("updated url = %q, want %q", got.URL, url2)
	}

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

	// Delete.
	call("DELETE", "/monitors/"+id, "", 204, nil)
	call("GET", "/monitors/"+id, "", 404, nil)
	call("DELETE", "/monitors/"+id, "", 404, nil)
	call("GET", "/monitors/"+id+"/results", "", 404, nil)
}
