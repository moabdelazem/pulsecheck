package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moabdelazem/pulsecheck-checker/internal/check"
	"github.com/moabdelazem/pulsecheck-checker/internal/store"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	// How often to look for due monitors. Each monitor has its own interval.
	poll := durationEnv("POLL_INTERVAL", 5*time.Second)
	retention := durationEnv("RETENTION", 30*24*time.Hour)
	if retention < 24*time.Hour {
		// Guards against typos like "30m" meant as 30 days, which would wipe history.
		slog.Error("RETENTION must be at least 24h (Go duration, e.g. 720h for 30 days)", "value", retention)
		os.Exit(1)
	}
	opsAddr := os.Getenv("OPS_ADDR")
	if opsAddr == "" {
		opsAddr = ":9090"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		slog.Error("invalid DATABASE_URL", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.Ping(ctx); err != nil {
		slog.Error("cannot reach database", "err", err)
		os.Exit(1)
	}

	// A round takes at most ~1 minute (client timeout), so this only trips on a stuck loop.
	h := &health{db: db, maxAge: 2*poll + 90*time.Second}
	h.roundDone(time.Now())
	ops := &http.Server{Addr: opsAddr, Handler: h.mux(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := ops.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			slog.Error("ops server", "err", err)
			stop() // without health and metrics the checker should not keep running unnoticed
		}
	}()

	var bg sync.WaitGroup
	bg.Go(func() { cleanupLoop(ctx, db, retention) })

	// Per-check timeouts come from each monitor; this is only a hard upper limit.
	client := &http.Client{Timeout: time.Minute}
	slog.Info("checker started", "poll_interval", poll, "retention", retention, "ops_addr", opsAddr)

	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		runChecks(ctx, db, client)
		h.roundDone(time.Now())
		select {
		case <-ctx.Done():
			bg.Wait()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ops.Shutdown(shutdownCtx)
			slog.Info("checker stopped")
			return
		case <-ticker.C:
		}
	}
}

// shortcut: one goroutine per monitor and each round waits for its slowest check
// (max 30s timeout), add a worker pool with in-flight tracking past ~thousands of monitors.
func runChecks(ctx context.Context, db *pgxpool.Pool, client *http.Client) {
	monitors, err := store.DueMonitors(ctx, db)
	if err != nil {
		if ctx.Err() == nil {
			errorsTotal.WithLabelValues("load_monitors").Inc()
			slog.Error("load monitors", "err", err)
		}
		return
	}

	var wg sync.WaitGroup
	for _, m := range monitors {
		wg.Go(func() {
			t := check.Target{
				URL:     m.URL,
				Method:  m.Method,
				Timeout: time.Duration(m.TimeoutSeconds) * time.Second,
			}
			if m.ExpectedStatus != nil {
				t.ExpectedStatus = *m.ExpectedStatus
			}
			r := check.Check(ctx, client, t)
			if ctx.Err() != nil {
				return // shutting down: the check was cut short, don't record it
			}
			checkDuration.Observe(r.Latency.Seconds())
			if r.Up {
				checksTotal.WithLabelValues("up").Inc()
				slog.Info("up", "url", r.URL, "status", r.StatusCode, "latency", r.Latency)
			} else {
				checksTotal.WithLabelValues("down").Inc()
				slog.Warn("down", "url", r.URL, "status", r.StatusCode, "latency", r.Latency, "err", r.Err)
			}
			if err := store.SaveResult(ctx, db, m.ID, r); err != nil {
				errorsTotal.WithLabelValues("save_result").Inc()
				slog.Error("save result", "url", m.URL, "err", err)
			}
		})
	}
	wg.Wait()
}

// cleanupLoop deletes results older than retention, at startup and then hourly.
// Running several checkers is safe: they just delete the same rows.
func cleanupLoop(ctx context.Context, db *pgxpool.Pool, retention time.Duration) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		n, err := store.DeleteOldResults(ctx, db, time.Now().Add(-retention))
		resultsDeleted.Add(float64(n))
		switch {
		case err != nil && ctx.Err() == nil:
			errorsTotal.WithLabelValues("cleanup").Inc()
			slog.Error("delete old results", "err", err)
		case n > 0:
			slog.Info("deleted old results", "rows", n, "older_than", retention)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func durationEnv(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		slog.Error("invalid duration", "key", key, "value", v, "err", err)
		os.Exit(1)
	}
	return d
}
