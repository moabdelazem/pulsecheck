package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moabdelazem/pulsecheck-api/internal/server"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
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

	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(db),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	slog.Info("api listening", "addr", addr)

	select {
	case err := <-errCh:
		slog.Error("server", "err", err) // e.g. port already in use
		os.Exit(1)
	case <-ctx.Done():
	}

	// Give in-flight requests up to 10s to finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
	}
	slog.Info("api stopped")
}
