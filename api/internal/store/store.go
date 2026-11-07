// Package store holds the Postgres queries for monitors and their check results.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("a monitor with this url already exists")
)

// Settings are the user-editable fields of a monitor.
type Settings struct {
	URL             string `json:"url"`
	Name            string `json:"name"`
	Method          string `json:"method"`
	IntervalSeconds int    `json:"interval_seconds"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	ExpectedStatus  *int   `json:"expected_status"` // nil = any 2xx or 3xx
	Paused          bool   `json:"paused"`
}

// DefaultSettings match the column defaults in scripts/dev/schema.sql.
func DefaultSettings() Settings {
	return Settings{Method: "GET", IntervalSeconds: 30, TimeoutSeconds: 5}
}

type Monitor struct {
	ID int64 `json:"id"`
	Settings
	CreatedAt time.Time `json:"created_at"`
}

type Result struct {
	ID         int64     `json:"id"`
	Up         bool      `json:"up"`
	StatusCode *int      `json:"status_code"`
	LatencyMS  int       `json:"latency_ms"`
	Error      *string   `json:"error"`
	CheckedAt  time.Time `json:"checked_at"`
}

// Bucket summarises the checks in one time slot of a latency chart.
type Bucket struct {
	Time         time.Time `json:"time"`
	Checks       int64     `json:"checks"`
	Down         int64     `json:"down"`
	AvgLatencyMS *float64  `json:"avg_latency_ms"` // of successful checks; nil if none
}

type Stats struct {
	Checks        int64   `json:"checks"`
	UpChecks      int64   `json:"up_checks"`
	UptimePercent float64 `json:"uptime_percent"`
	AvgLatencyMS  float64 `json:"avg_latency_ms"`
}

const monitorCols = `id, url, name, method, interval_seconds, timeout_seconds, expected_status, paused, created_at`

func ListMonitors(ctx context.Context, db *pgxpool.Pool) ([]Monitor, error) {
	rows, err := db.Query(ctx, `SELECT `+monitorCols+` FROM monitors ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Monitor])
}

func GetMonitor(ctx context.Context, db *pgxpool.Pool, id int64) (Monitor, error) {
	rows, _ := db.Query(ctx, `SELECT `+monitorCols+` FROM monitors WHERE id = $1`, id)
	return oneMonitor(rows)
}

func CreateMonitor(ctx context.Context, db *pgxpool.Pool, s Settings) (Monitor, error) {
	rows, _ := db.Query(ctx, `
		INSERT INTO monitors (url, name, method, interval_seconds, timeout_seconds, expected_status, paused)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+monitorCols,
		s.URL, s.Name, s.Method, s.IntervalSeconds, s.TimeoutSeconds, s.ExpectedStatus, s.Paused)
	return oneMonitor(rows)
}

func UpdateMonitor(ctx context.Context, db *pgxpool.Pool, id int64, s Settings) (Monitor, error) {
	rows, _ := db.Query(ctx, `
		UPDATE monitors
		SET url = $2, name = $3, method = $4, interval_seconds = $5, timeout_seconds = $6,
		    expected_status = $7, paused = $8
		WHERE id = $1
		RETURNING `+monitorCols,
		id, s.URL, s.Name, s.Method, s.IntervalSeconds, s.TimeoutSeconds, s.ExpectedStatus, s.Paused)
	return oneMonitor(rows)
}

// DeleteMonitor also deletes the monitor's results (ON DELETE CASCADE).
func DeleteMonitor(ctx context.Context, db *pgxpool.Pool, id int64) error {
	tag, err := db.Exec(ctx, `DELETE FROM monitors WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func ListResults(ctx context.Context, db *pgxpool.Pool, monitorID int64, limit int) ([]Result, error) {
	rows, err := db.Query(ctx, `
		SELECT id, up, status_code, latency_ms, error, checked_at
		FROM check_results
		WHERE monitor_id = $1
		ORDER BY checked_at DESC
		LIMIT $2`, monitorID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Result])
}

// LatencyBuckets groups results since `since` into slots of `bucket` length.
// Slots without any checks are left out.
func LatencyBuckets(ctx context.Context, db *pgxpool.Pool, monitorID int64, since time.Time, bucket time.Duration) ([]Bucket, error) {
	rows, err := db.Query(ctx, `
		SELECT date_bin(make_interval(secs => $3), checked_at, $2) AS slot,
		       count(*),
		       count(*) FILTER (WHERE NOT up),
		       (avg(latency_ms) FILTER (WHERE up))::float8
		FROM check_results
		WHERE monitor_id = $1 AND checked_at >= $2
		GROUP BY slot
		ORDER BY slot`, monitorID, since, bucket.Seconds())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Bucket])
}

func GetStats(ctx context.Context, db *pgxpool.Pool, monitorID int64, since time.Time) (Stats, error) {
	var s Stats
	err := db.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE up), COALESCE(avg(latency_ms), 0)::float8
		FROM check_results
		WHERE monitor_id = $1 AND checked_at >= $2`, monitorID, since).
		Scan(&s.Checks, &s.UpChecks, &s.AvgLatencyMS)
	if err != nil {
		return Stats{}, err
	}
	if s.Checks > 0 {
		s.UptimePercent = float64(s.UpChecks) * 100 / float64(s.Checks)
	}
	return s, nil
}

// oneMonitor maps "no row" and "url already taken" to the package errors.
func oneMonitor(rows pgx.Rows) (Monitor, error) {
	m, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[Monitor])
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Monitor{}, ErrNotFound
	case errors.As(err, &pgErr) && pgErr.Code == "23505": // unique_violation
		return Monitor{}, ErrDuplicate
	}
	return m, err
}
