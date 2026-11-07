// Package web serves the dashboard pages. All data comes from the API.
package web

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/moabdelazem/pulsecheck-dashboard/internal/apiclient"
)

//go:embed templates/*.html
var templates embed.FS

type handler struct {
	api    *apiclient.Client
	index  *template.Template
	detail *template.Template
}

func New(api *apiclient.Client) http.Handler {
	h := &handler{api: api, index: page("index.html"), detail: page("detail.html")}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /{$}", h.overview)
	mux.HandleFunc("GET /monitors/{id}", h.monitor)
	mux.HandleFunc("POST /monitors", h.create)
	mux.HandleFunc("POST /monitors/{id}/delete", h.delete)

	// Rejects form posts coming from other sites (CSRF).
	return http.NewCrossOriginProtection().Handler(mux)
}

func page(name string) *template.Template {
	funcs := template.FuncMap{"ago": ago}
	return template.Must(template.New("").Funcs(funcs).ParseFS(templates, "templates/layout.html", "templates/"+name))
}

// Data shared by every page (used by layout.html).
type pageData struct {
	Title   string
	Error   string
	Refresh int // seconds, 0 = no auto refresh
}

type row struct {
	Monitor apiclient.Monitor
	Last    *apiclient.Result // nil until the checker has run
	Day     apiclient.Stats
}

type overviewData struct {
	pageData
	Rows           []row
	Up, Down, Wait int
}

func (h *handler) overview(w http.ResponseWriter, r *http.Request) {
	data := overviewData{pageData: pageData{Title: "Monitors", Error: r.URL.Query().Get("error"), Refresh: 30}}

	monitors, err := h.api.ListMonitors(r.Context())
	if err != nil {
		data.Error = h.userMessage(err)
		h.render(w, http.StatusBadGateway, h.index, data)
		return
	}

	// shortcut: 2 API calls per monitor, add a summary endpoint to the API past ~100 monitors.
	data.Rows = make([]row, len(monitors))
	errs := make([]error, len(monitors))
	var wg sync.WaitGroup
	for i, m := range monitors {
		wg.Go(func() {
			data.Rows[i], errs[i] = h.loadRow(r.Context(), m)
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		data.Error = h.userMessage(err)
	}

	for _, rw := range data.Rows {
		switch {
		case rw.Last == nil:
			data.Wait++
		case rw.Last.Up:
			data.Up++
		default:
			data.Down++
		}
	}
	h.render(w, http.StatusOK, h.index, data)
}

func (h *handler) loadRow(ctx context.Context, m apiclient.Monitor) (row, error) {
	rw := row{Monitor: m}
	last, err := h.api.Results(ctx, m.ID, 1)
	if err != nil {
		return rw, err
	}
	if len(last) > 0 {
		rw.Last = &last[0]
	}
	rw.Day, err = h.api.Stats(ctx, m.ID, 24*time.Hour)
	return rw, err
}

type detailData struct {
	pageData
	Monitor   apiclient.Monitor
	Results   []apiclient.Result // newest first
	Strip     []apiclient.Result // oldest first, for the status bar
	Day, Week apiclient.Stats
}

func (h *handler) monitor(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	data := detailData{pageData: pageData{Refresh: 30}}

	data.Monitor, err = h.api.GetMonitor(ctx, id)
	if errors.Is(err, apiclient.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err == nil {
		data.Results, err = h.api.Results(ctx, id, 50)
	}
	if err == nil {
		data.Day, err = h.api.Stats(ctx, id, 24*time.Hour)
	}
	if err == nil {
		data.Week, err = h.api.Stats(ctx, id, 7*24*time.Hour)
	}
	if err != nil {
		data.Title = "Monitor"
		data.Error = h.userMessage(err)
		h.render(w, http.StatusBadGateway, h.detail, data)
		return
	}

	data.Title = data.Monitor.URL
	data.Strip = slices.Clone(data.Results)
	slices.Reverse(data.Strip)
	h.render(w, http.StatusOK, h.detail, data)
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if _, err := h.api.CreateMonitor(r.Context(), r.PostFormValue("url")); err != nil {
		redirectWithError(w, r, h.userMessage(err))
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	// Already gone counts as success.
	if err := h.api.DeleteMonitor(r.Context(), id); err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		redirectWithError(w, r, h.userMessage(err))
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	if err := h.api.Health(r.Context()); err != nil {
		http.Error(w, "api unreachable", http.StatusServiceUnavailable)
		return
	}
	fmt.Fprintln(w, "ok")
}

// userMessage shows the API's own message for bad input (4xx) and hides everything else.
func (h *handler) userMessage(err error) string {
	var apiErr *apiclient.APIError
	if errors.As(err, &apiErr) && apiErr.Status < 500 {
		return apiErr.Message
	}
	slog.Error("api call failed", "err", err)
	return "The API is not responding right now. This page will retry automatically."
}

func redirectWithError(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/?error="+url.QueryEscape(msg), http.StatusSeeOther)
}

// render executes into a buffer first so a template error becomes a clean 500.
func (h *handler) render(w http.ResponseWriter, status int, t *template.Template, data any) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		slog.Error("render", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

// ago formats a time as "12s ago", "5m ago", "3h ago" or "2d ago".
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
