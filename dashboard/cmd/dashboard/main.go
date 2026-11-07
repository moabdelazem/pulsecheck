package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moabdelazem/pulsecheck-dashboard/internal/apiclient"
	"github.com/moabdelazem/pulsecheck-dashboard/internal/web"
)

func main() {
	apiURL := envOr("API_URL", "http://localhost:8080")
	addr := envOr("ADDR", ":3000")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr:              addr,
		Handler:           web.New(apiclient.New(apiURL)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      20 * time.Second, // pages wait on API calls with a 5s timeout each
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	slog.Info("dashboard listening", "addr", addr, "api", apiURL)

	select {
	case err := <-errCh:
		slog.Error("server", "err", err)
		os.Exit(1)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
	}
	slog.Info("dashboard stopped")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
