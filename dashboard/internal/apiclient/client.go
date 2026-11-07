// Package apiclient is a small client for the Pulse Check API.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrNotFound = errors.New("monitor not found")

// APIError is a non-2xx answer from the API, carrying its {"error": "..."} message.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }

type Monitor struct {
	ID              int64     `json:"id"`
	URL             string    `json:"url"`
	Name            string    `json:"name"`
	Method          string    `json:"method"`
	IntervalSeconds int       `json:"interval_seconds"`
	TimeoutSeconds  int       `json:"timeout_seconds"`
	ExpectedStatus  *int      `json:"expected_status"`
	Paused          bool      `json:"paused"`
	CreatedAt       time.Time `json:"created_at"`
}

// Label is the name if set, otherwise the URL.
func (m Monitor) Label() string {
	if m.Name != "" {
		return m.Name
	}
	return m.URL
}

// Bucket is one time slot of the latency chart.
type Bucket struct {
	Time         time.Time `json:"time"`
	Checks       int64     `json:"checks"`
	Down         int64     `json:"down"`
	AvgLatencyMS *float64  `json:"avg_latency_ms"`
}

type Result struct {
	ID         int64     `json:"id"`
	Up         bool      `json:"up"`
	StatusCode *int      `json:"status_code"`
	LatencyMS  int       `json:"latency_ms"`
	Error      *string   `json:"error"`
	CheckedAt  time.Time `json:"checked_at"`
}

type Stats struct {
	Checks        int64   `json:"checks"`
	UpChecks      int64   `json:"up_checks"`
	UptimePercent float64 `json:"uptime_percent"`
	AvgLatencyMS  float64 `json:"avg_latency_ms"`
}

type Client struct {
	base string
	http *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		base: strings.TrimRight(baseURL, "/"),
		http: &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *Client) Health(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/healthz", nil, nil)
}

func (c *Client) ListMonitors(ctx context.Context) ([]Monitor, error) {
	var m []Monitor
	return m, c.do(ctx, http.MethodGet, "/monitors", nil, &m)
}

func (c *Client) GetMonitor(ctx context.Context, id int64) (Monitor, error) {
	var m Monitor
	return m, c.do(ctx, http.MethodGet, "/monitors/"+itoa(id), nil, &m)
}

// CreateMonitor sends the given fields; the API fills in defaults for the rest.
func (c *Client) CreateMonitor(ctx context.Context, fields map[string]any) (Monitor, error) {
	var m Monitor
	return m, c.do(ctx, http.MethodPost, "/monitors", fields, &m)
}

// UpdateMonitor changes only the given fields (a nil value sends JSON null).
func (c *Client) UpdateMonitor(ctx context.Context, id int64, fields map[string]any) (Monitor, error) {
	var m Monitor
	return m, c.do(ctx, http.MethodPatch, "/monitors/"+itoa(id), fields, &m)
}

func (c *Client) DeleteMonitor(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, "/monitors/"+itoa(id), nil, nil)
}

// Results returns the newest results first.
func (c *Client) Results(ctx context.Context, id int64, limit int) ([]Result, error) {
	var r []Result
	path := "/monitors/" + itoa(id) + "/results?limit=" + strconv.Itoa(limit)
	return r, c.do(ctx, http.MethodGet, path, nil, &r)
}

func (c *Client) Stats(ctx context.Context, id int64, window time.Duration) (Stats, error) {
	var body struct {
		Stats Stats `json:"stats"`
	}
	path := "/monitors/" + itoa(id) + "/stats?window=" + url.QueryEscape(window.String())
	err := c.do(ctx, http.MethodGet, path, nil, &body)
	return body.Stats, err
}

func (c *Client) Latency(ctx context.Context, id int64, window, bucket time.Duration) ([]Bucket, error) {
	var body struct {
		Buckets []Bucket `json:"buckets"`
	}
	path := "/monitors/" + itoa(id) + "/latency?window=" + url.QueryEscape(window.String()) +
		"&bucket=" + url.QueryEscape(bucket.String())
	err := c.do(ctx, http.MethodGet, path, nil, &body)
	return body.Buckets, err
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("api unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&e) != nil || e.Error == "" {
			e.Error = resp.Status
		}
		return &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	return nil
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }
