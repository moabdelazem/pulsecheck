package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/moabdelazem/pulsecheck-dashboard/internal/apiclient"
)

// fakeAPI imitates the Pulse Check API with one monitor that has two results.
func fakeAPI(t *testing.T) *httptest.Server {
	now := time.Now()
	mux := http.NewServeMux()
	send := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /monitors", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, []map[string]any{{"id": 1, "url": "https://up.example.com", "created_at": now}})
	})
	mux.HandleFunc("GET /monitors/{id}", func(w http.ResponseWriter, r *http.Request) {
		send(w, 404, map[string]string{"error": "monitor not found"})
	})
	mux.HandleFunc("GET /monitors/1", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, map[string]any{"id": 1, "url": "https://up.example.com", "created_at": now})
	})
	mux.HandleFunc("GET /monitors/1/results", func(w http.ResponseWriter, r *http.Request) {
		results := []map[string]any{
			{"id": 2, "up": true, "status_code": 200, "latency_ms": 120, "checked_at": now},
			{"id": 1, "up": false, "status_code": nil, "latency_ms": 5000, "error": "timeout", "checked_at": now.Add(-time.Minute)},
		}
		if r.URL.Query().Get("limit") == "1" {
			results = results[:1]
		}
		send(w, 200, results)
	})
	mux.HandleFunc("GET /monitors/1/stats", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, map[string]any{"stats": map[string]any{"checks": 200, "up_checks": 199, "uptime_percent": 99.5, "avg_latency_ms": 123.4}})
	})
	mux.HandleFunc("POST /monitors", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ URL string }
		json.NewDecoder(r.Body).Decode(&body)
		if body.URL == "https://dup.example.com" {
			send(w, 409, map[string]string{"error": "a monitor with this url already exists"})
			return
		}
		send(w, 201, map[string]any{"id": 2, "url": body.URL, "created_at": now})
	})
	mux.HandleFunc("DELETE /monitors/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
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
	h := New(apiclient.New(fakeAPI(t).URL))
	rec := get(t, h, "/")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "https://up.example.com", `class="pill up"`, "99.5%", "123 ms", `href="/monitors/1"`)
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
	h := New(apiclient.New(fakeAPI(t).URL))
	rec := get(t, h, "/monitors/1")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	mustContain(t, rec.Body.String(), "https://up.example.com", "timeout", "5000 ms", "99.50%", `class="card strip"`)

	if rec := get(t, h, "/monitors/99"); rec.Code != 404 {
		t.Errorf("unknown monitor: status %d, want 404", rec.Code)
	}
	if rec := get(t, h, "/monitors/abc"); rec.Code != 404 {
		t.Errorf("bad id: status %d, want 404", rec.Code)
	}
}

func TestCreate(t *testing.T) {
	h := New(apiclient.New(fakeAPI(t).URL))

	rec := postForm(t, h, "/monitors", url.Values{"url": {"https://new.example.com"}})
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

func TestDelete(t *testing.T) {
	h := New(apiclient.New(fakeAPI(t).URL))
	rec := postForm(t, h, "/monitors/1/delete", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", rec.Code)
	}
}

func TestCrossSitePostRejected(t *testing.T) {
	h := New(apiclient.New(fakeAPI(t).URL))
	req := httptest.NewRequest("POST", "/monitors/1/delete", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
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
