package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLivez(t *testing.T) {
	h := &health{maxAge: time.Minute}

	h.roundDone(time.Now())
	rec := httptest.NewRecorder()
	h.mux().ServeHTTP(rec, httptest.NewRequest("GET", "/livez", nil))
	if rec.Code != 200 {
		t.Fatalf("fresh round: status %d, want 200", rec.Code)
	}

	h.roundDone(time.Now().Add(-2 * time.Minute))
	rec = httptest.NewRecorder()
	h.mux().ServeHTTP(rec, httptest.NewRequest("GET", "/livez", nil))
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "stalled") {
		t.Fatalf("stale round: status %d %q, want 503 stalled", rec.Code, rec.Body.String())
	}
}

func TestMetricsEndpoint(t *testing.T) {
	checksTotal.WithLabelValues("up").Inc()
	rec := httptest.NewRecorder()
	(&health{}).mux().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	for _, want := range []string{`pulsecheck_checks_total{result="up"}`, "pulsecheck_check_duration_seconds", "go_goroutines"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}
