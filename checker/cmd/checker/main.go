package main

import (
	"context"
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
	interval := durationEnv("CHECK_INTERVAL", 30*time.Second)
	timeout := durationEnv("CHECK_TIMEOUT", 5*time.Second)

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

	client := &http.Client{Timeout: timeout}
	slog.Info("checker started", "interval", interval, "timeout", timeout)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		runChecks(ctx, db, client)
		select {
		case <-ctx.Done():
			slog.Info("checker stopped")
			return
		case <-ticker.C:
		}
	}
}

// shortcut: one goroutine per monitor, add a worker pool if the list grows to thousands.
func runChecks(ctx context.Context, db *pgxpool.Pool, client *http.Client) {
	monitors, err := store.Monitors(ctx, db)
	if err != nil {
		slog.Error("load monitors", "err", err)
		return
	}
	if len(monitors) == 0 {
		slog.Info("no monitors to check")
		return
	}

	var wg sync.WaitGroup
	for _, m := range monitors {
		wg.Go(func() {
			r := check.Check(ctx, client, m.URL)
			if ctx.Err() != nil {
				return // shutting down: the check was cut short, don't record it
			}
			if r.Up() {
				slog.Info("up", "url", r.URL, "status", r.StatusCode, "latency", r.Latency)
			} else {
				slog.Warn("down", "url", r.URL, "status", r.StatusCode, "latency", r.Latency, "err", r.Err)
			}
			if err := store.SaveResult(ctx, db, m.ID, r); err != nil {
				slog.Error("save result", "url", m.URL, "err", err)
			}
		})
	}
	wg.Wait()
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
