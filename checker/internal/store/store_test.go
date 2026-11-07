package store

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Needs DATABASE_URL and the schema from scripts/dev/schema.sql (`make test` sets it).
func TestDueMonitors(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close) // registered first, so it runs after the cleanups below

	prefix := fmt.Sprintf("https://due-test-%d.example.com/", time.Now().UnixNano())
	t.Cleanup(func() {
		db.Exec(ctx, `DELETE FROM monitors WHERE url LIKE $1`, prefix+"%")
	})

	// name -> paused, interval, seconds since last check (-1 = never checked)
	cases := []struct {
		name     string
		paused   bool
		interval int
		lastAgo  int
		wantDue  bool
	}{
		{"never-checked", false, 30, -1, true},
		{"overdue", false, 60, 120, true},
		{"recent", false, 60, 5, false},
		{"paused", true, 30, -1, false},
	}
	ids := map[int64]string{}
	for _, c := range cases {
		var id int64
		err := db.QueryRow(ctx, `INSERT INTO monitors (url, paused, interval_seconds) VALUES ($1, $2, $3) RETURNING id`,
			prefix+c.name, c.paused, c.interval).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		ids[id] = c.name
		if c.lastAgo >= 0 {
			_, err = db.Exec(ctx, `INSERT INTO check_results (monitor_id, up, latency_ms, checked_at)
				VALUES ($1, true, 10, now() - make_interval(secs => $2))`, id, c.lastAgo)
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	due, err := DueMonitors(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range due {
		if name, ok := ids[m.ID]; ok {
			got = append(got, name)
		}
	}
	for _, c := range cases {
		if slices.Contains(got, c.name) != c.wantDue {
			t.Errorf("%s: due = %v, want %v", c.name, !c.wantDue, c.wantDue)
		}
	}
}

func TestDeleteOldResults(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close) // registered first, so it runs after the cleanups below

	url := fmt.Sprintf("https://cleanup-test-%d.example.com", time.Now().UnixNano())
	var id int64
	if err := db.QueryRow(ctx, `INSERT INTO monitors (url) VALUES ($1) RETURNING id`, url).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(ctx, `DELETE FROM monitors WHERE id = $1`, id) })

	// Rows far in the past, so the cutoff below never touches real data in a shared database.
	ancient := time.Now().AddDate(-200, 0, 0)
	_, err = db.Exec(ctx, `INSERT INTO check_results (monitor_id, up, latency_ms, checked_at) VALUES
		($1, true, 1, $2), ($1, true, 1, $2), ($1, true, 1, $3)`,
		id, ancient.AddDate(-1, 0, 0), ancient.AddDate(1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}

	n, err := DeleteOldResults(ctx, db, ancient)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("deleted %d rows, want 2", n)
	}
	var left int
	db.QueryRow(ctx, `SELECT count(*) FROM check_results WHERE monitor_id = $1`, id).Scan(&left)
	if left != 1 {
		t.Fatalf("%d rows left, want 1 (the newer one)", left)
	}
}
