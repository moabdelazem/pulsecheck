package main

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	checksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "pulsecheck_checks_total",
		Help: "Checks performed, by result (up or down).",
	}, []string{"result"})
	checkDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "pulsecheck_check_duration_seconds",
		Help:    "Time taken by each HTTP check.",
		Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30},
	})
	errorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "pulsecheck_checker_errors_total",
		Help: "Database errors, by operation.",
	}, []string{"op"})
	resultsDeleted = promauto.NewCounter(prometheus.CounterOpts{
		Name: "pulsecheck_results_deleted_total",
		Help: "Old check results deleted by the retention cleanup.",
	})
	lastRoundSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "pulsecheck_last_round_timestamp_seconds",
		Help: "Unix time when the last poll round finished.",
	})
)

// health serves /livez and /healthz for the checker.
type health struct {
	db        *pgxpool.Pool
	lastRound atomic.Int64 // unix nanoseconds
	maxAge    time.Duration
}

func (h *health) roundDone(t time.Time) {
	h.lastRound.Store(t.UnixNano())
	lastRoundSeconds.Set(float64(t.Unix()))
}

// livez fails when the poll loop has stopped finishing rounds, so an orchestrator restarts it.
func (h *health) livez(w http.ResponseWriter, r *http.Request) {
	age := time.Since(time.Unix(0, h.lastRound.Load()))
	if age > h.maxAge {
		http.Error(w, fmt.Sprintf("poll loop stalled: last round %s ago", age.Round(time.Second)), http.StatusServiceUnavailable)
		return
	}
	fmt.Fprintln(w, "ok")
}

// healthz also checks the database.
func (h *health) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.db.Ping(ctx); err != nil {
		http.Error(w, "database unreachable", http.StatusServiceUnavailable)
		return
	}
	h.livez(w, r)
}

func (h *health) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", h.livez)
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}
