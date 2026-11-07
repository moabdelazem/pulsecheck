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
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

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
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	mux.HandleFunc("GET /healthz", h.health)
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /{$}", h.overview)
	mux.HandleFunc("GET /monitors/{id}", h.monitor)
	mux.HandleFunc("POST /monitors", h.create)
	mux.HandleFunc("POST /monitors/{id}/settings", h.settings)
	mux.HandleFunc("POST /monitors/{id}/pause", h.setPaused(true))
	mux.HandleFunc("POST /monitors/{id}/resume", h.setPaused(false))
	mux.HandleFunc("POST /monitors/{id}/delete", h.delete)

	// Rejects form posts coming from other sites (CSRF).
	return instrument(http.NewCrossOriginProtection().Handler(mux))
}

func page(name string) *template.Template {
	funcs := template.FuncMap{
		"ago":    ago,
		"every":  every,
		"pill":   pill,
		"ranges": func() []chartRange { return chartRanges },
		"sub":    func(a, b float64) float64 { return a - b },
	}
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
	Rows                   []row
	Up, Down, Wait, Paused int
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
		case rw.Monitor.Paused:
			data.Paused++
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
	Range     chartRange
	Chart     chart
}

func (h *handler) monitor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	data := detailData{
		pageData: pageData{Error: r.URL.Query().Get("error"), Refresh: 30},
		Range:    findRange(r.URL.Query().Get("range")),
	}

	var err error
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
	var buckets []apiclient.Bucket
	if err == nil {
		buckets, err = h.api.Latency(ctx, id, data.Range.Window, data.Range.Bucket)
	}
	if err != nil {
		data.Title = "Monitor"
		data.Error = h.userMessage(err)
		h.render(w, http.StatusBadGateway, h.detail, data)
		return
	}

	data.Title = data.Monitor.Label()
	data.Strip = slices.Clone(data.Results)
	slices.Reverse(data.Strip)
	data.Chart = buildChart(buckets, data.Range, time.Now())
	h.render(w, http.StatusOK, h.detail, data)
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	fields := map[string]any{"url": r.PostFormValue("url")}
	if name := strings.TrimSpace(r.PostFormValue("name")); name != "" {
		fields["name"] = name
	}
	if v := r.PostFormValue("interval_seconds"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			redirectWithError(w, r, "/", "Interval must be a whole number of seconds.")
			return
		}
		fields["interval_seconds"] = n
	}
	if _, err := h.api.CreateMonitor(r.Context(), fields); err != nil {
		redirectWithError(w, r, "/", h.userMessage(err))
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// settings saves the edit form on the detail page. The API validates the values.
func (h *handler) settings(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	back := "/monitors/" + strconv.FormatInt(id, 10)
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)

	fields := map[string]any{
		"name":   strings.TrimSpace(r.PostFormValue("name")),
		"method": r.PostFormValue("method"),
	}
	for _, f := range []struct{ key, label string }{
		{"interval_seconds", "Interval"},
		{"timeout_seconds", "Timeout"},
	} {
		n, err := strconv.Atoi(r.PostFormValue(f.key))
		if err != nil {
			redirectWithError(w, r, back, f.label+" must be a whole number of seconds.")
			return
		}
		fields[f.key] = n
	}
	// Empty means "any 2xx or 3xx", sent as JSON null.
	fields["expected_status"] = nil
	if v := strings.TrimSpace(r.PostFormValue("expected_status")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			redirectWithError(w, r, back, "Expected status must be a number like 200, or empty.")
			return
		}
		fields["expected_status"] = n
	}

	if _, err := h.api.UpdateMonitor(r.Context(), id, fields); err != nil {
		redirectWithError(w, r, back, h.userMessage(err))
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// setPaused handles pause and resume. The form may ask to go back to the overview.
func (h *handler) setPaused(paused bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		back := "/monitors/" + strconv.FormatInt(id, 10)
		if r.PostFormValue("back") == "/" {
			back = "/"
		}
		if _, err := h.api.UpdateMonitor(r.Context(), id, map[string]any{"paused": paused}); err != nil {
			redirectWithError(w, r, back, h.userMessage(err))
			return
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
	}
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	// Already gone counts as success.
	if err := h.api.DeleteMonitor(r.Context(), id); err != nil && !errors.Is(err, apiclient.ErrNotFound) {
		redirectWithError(w, r, "/", h.userMessage(err))
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
	if errors.Is(err, apiclient.ErrNotFound) {
		return "Monitor not found."
	}
	slog.Error("api call failed", "err", err)
	return "The API is not responding right now. This page will retry automatically."
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

// redirectWithError sends the user back to a page of ours with a message to show.
func redirectWithError(w http.ResponseWriter, r *http.Request, path, msg string) {
	http.Redirect(w, r, path+"?error="+url.QueryEscape(msg), http.StatusSeeOther)
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

// pill is the status badge. Paused wins over the last result; nil means never checked.
func pill(paused bool, last *apiclient.Result) template.HTML {
	switch {
	case paused:
		return `<span class="pill paused">Paused</span>`
	case last == nil:
		return `<span class="pill wait">Pending</span>`
	case last.Up:
		return `<span class="pill up">Up</span>`
	default:
		return `<span class="pill down">Down</span>`
	}
}

// every formats an interval in seconds as "30s", "5m" or "2h".
func every(seconds int) string {
	switch {
	case seconds%3600 == 0:
		return fmt.Sprintf("%dh", seconds/3600)
	case seconds%60 == 0:
		return fmt.Sprintf("%dm", seconds/60)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
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
