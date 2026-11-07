// Package store reads monitors from and writes check results to Postgres.
package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moabdelazem/pulsecheck-checker/internal/check"
)

type Monitor struct {
	ID  int64
	URL string
}

func Monitors(ctx context.Context, db *pgxpool.Pool) ([]Monitor, error) {
	rows, err := db.Query(ctx, `SELECT id, url FROM monitors ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Monitor])
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
		monitorID, r.Up(), status, r.Latency.Milliseconds(), errText, r.CheckedAt)
	return err
}
