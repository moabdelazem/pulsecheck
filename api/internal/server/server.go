// Package server is the HTTP API for managing monitors and reading their results.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moabdelazem/pulsecheck-api/internal/store"
)

type server struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) http.Handler {
	s := &server{db: db}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /monitors", s.listMonitors)
	mux.HandleFunc("POST /monitors", s.createMonitor)
	mux.HandleFunc("GET /monitors/{id}", s.getMonitor)
	mux.HandleFunc("PUT /monitors/{id}", s.updateMonitor)
	mux.HandleFunc("DELETE /monitors/{id}", s.deleteMonitor)
	mux.HandleFunc("GET /monitors/{id}/results", s.listResults)
	mux.HandleFunc("GET /monitors/{id}/stats", s.stats)
	return mux
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) listMonitors(w http.ResponseWriter, r *http.Request) {
	monitors, err := store.ListMonitors(r.Context(), s.db)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, monitors)
}

func (s *server) createMonitor(w http.ResponseWriter, r *http.Request) {
	u, ok := readURL(w, r)
	if !ok {
		return
	}
	m, err := store.CreateMonitor(r.Context(), s.db, u)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *server) getMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	m, err := store.GetMonitor(r.Context(), s.db, id)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *server) updateMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	u, ok := readURL(w, r)
	if !ok {
		return
	}
	m, err := store.UpdateMonitor(r.Context(), s.db, id, u)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *server) deleteMonitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := store.DeleteMonitor(r.Context(), s.db, id); err != nil {
		s.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /monitors/{id}/results?limit=50 (newest first, limit 1..500)
func (s *server) listResults(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeError(w, http.StatusBadRequest, "limit must be a number between 1 and 500")
			return
		}
		limit = n
	}
	if _, err := store.GetMonitor(r.Context(), s.db, id); err != nil {
		s.storeError(w, err)
		return
	}
	results, err := store.ListResults(r.Context(), s.db, id, limit)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// GET /monitors/{id}/stats?window=24h (any Go duration up to 90 days)
func (s *server) stats(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	window := 24 * time.Hour
	if v := r.URL.Query().Get("window"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 || d > 90*24*time.Hour {
			writeError(w, http.StatusBadRequest, `window must be a duration like "1h" or "720h" (max 2160h)`)
			return
		}
		window = d
	}
	if _, err := store.GetMonitor(r.Context(), s.db, id); err != nil {
		s.storeError(w, err)
		return
	}
	st, err := store.GetStats(r.Context(), s.db, id, time.Now().Add(-window))
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"window": window.String(), "stats": st})
}

// storeError turns store errors into HTTP responses; unknown errors are logged, not shown.
func (s *server) storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "monitor not found")
	case errors.Is(err, store.ErrDuplicate):
		writeError(w, http.StatusConflict, err.Error())
	default:
		slog.Error("store", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "id must be a positive number")
		return 0, false
	}
	return id, true
}

// readURL decodes {"url": "..."} and checks it is an absolute http(s) URL.
func readURL(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		URL string `json:"url"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, `body must be JSON like {"url": "https://example.com"}`)
		return "", false
	}
	if err := validateURL(body.URL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", false
	}
	return body.URL, true
}

func validateURL(s string) error {
	if len(s) > 2048 {
		return errors.New("url is too long (max 2048 characters)")
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("url must be an absolute http or https URL, got %q", s)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
