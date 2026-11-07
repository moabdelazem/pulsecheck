// Package store reads monitors from and writes check results to Postgres.
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moabdelazem/pulsecheck-checker/internal/check"
)

type Monitor struct {
	ID             int64
	URL            string
	Method         string
	TimeoutSeconds int
	ExpectedStatus *int
}

// DueMonitors returns monitors that are not paused and whose last check is at
// least interval_seconds old (or that were never checked).
func DueMonitors(ctx context.Context, db *pgxpool.Pool) ([]Monitor, error) {
	// The 1 second slack stops a monitor from slipping a whole poll round when the
	// previous check started a few milliseconds after the round did.
	rows, err := db.Query(ctx, `
		SELECT m.id, m.url, m.method, m.timeout_seconds, m.expected_status
		FROM monitors m
		LEFT JOIN LATERAL (
			SELECT checked_at FROM check_results r
			WHERE r.monitor_id = m.id
			ORDER BY checked_at DESC
			LIMIT 1
		) last ON true
		WHERE NOT m.paused
		  AND (last.checked_at IS NULL
		       OR last.checked_at <= now() - make_interval(secs => m.interval_seconds - 1))
		ORDER BY m.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Monitor])
}

// DeleteOldResults deletes results checked before `before`, in batches so no single
// statement holds locks for long. It returns how many rows were deleted.
func DeleteOldResults(ctx context.Context, db *pgxpool.Pool, before time.Time) (int64, error) {
	const batch = 5000
	var total int64
	for {
		tag, err := db.Exec(ctx, `
			DELETE FROM check_results
			WHERE id IN (SELECT id FROM check_results WHERE checked_at < $1 LIMIT $2)`, before, batch)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < batch {
			return total, nil
		}
	}
}

func SaveResult(ctx context.Context, db *pgxpool.Pool, monitorID int64, r check.Result) error {
	// NULL instead of 0 / "" when the request never got a response.
	var status *int
	if r.StatusCode != 0 {
		status = &r.StatusCode
	}
	var errText *string
	if r.Err != nil {
		s := r.Err.Error()
		errText = &s
	}

	_, err := db.Exec(ctx, `
		INSERT INTO check_results (monitor_id, up, status_code, latency_ms, error, checked_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		monitorID, r.Up, status, r.Latency.Milliseconds(), errText, r.CheckedAt)
	return err
}
