# Pulse Check - Checker

Background worker that checks monitors on their own schedule and stores each
result in Postgres. Monitors and their settings are managed through the
[API](../api/) or the [dashboard](../dashboard/).

## How it works

Every `POLL_INTERVAL` (and once right at startup) the checker:

1. Loads the monitors that are **due**: not paused, and last checked at least
   `interval_seconds` ago (or never checked).
2. Checks them all in parallel, each with its own `method` (`GET` or `HEAD`) and
   `timeout_seconds`.
3. Saves one row per check to `check_results`.

A check counts as **up** when the site answers with the monitor's
`expected_status`, or with any `2xx`/`3xx` status if none is set. Redirects are
followed. Timeouts, DNS errors, refused connections and other statuses count as
**down**, and the reason is saved in the result's `error`.

Once at startup and then every hour it deletes results older than `RETENTION`,
5000 rows at a time so the table is never locked for long.

On `Ctrl+C` / `SIGTERM` it stops cleanly and does not save checks that were cut
short.

## Run

```sh
../scripts/dev/database.sh   # once: start Postgres and create the tables
make run
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | dev database (set by the Makefile) | Postgres connection string. Required when running the binary directly. |
| `POLL_INTERVAL` | `5s` | How often to look for due monitors (Go duration). A monitor is checked at most this much later than its interval. |
| `RETENTION` | `720h` | Delete results older than this (Go duration; `720h` = 30 days). Must be at least `24h`, so a typo like `30m` cannot wipe the history. |
| `OPS_ADDR` | `:9090` | Address of the health and metrics server. |

Intervals, timeouts, methods and expected statuses are set per monitor, not here.

## Health and metrics

The checker has no other HTTP traffic, so it runs a small server on `OPS_ADDR` just for these:

| Endpoint | Meaning | Use it for |
|---|---|---|
| `/livez` | The process is running (the checker also fails it if its poll loop stalls) | Liveness probe: restart on failure |
| `/healthz` | `/livez` plus its dependency (Postgres) is reachable | Readiness probe: stop sending traffic on failure |
| `/metrics` | Prometheus metrics | Scraping |

`/livez` fails when no poll round has finished for `2 × POLL_INTERVAL + 90s`.

| Metric | Type | Meaning |
|---|---|---|
| `pulsecheck_checks_total{result}` | counter | Checks done, `result` is `up` or `down` |
| `pulsecheck_check_duration_seconds` | histogram | Time per HTTP check |
| `pulsecheck_checker_errors_total{op}` | counter | Database errors: `load_monitors`, `save_result`, `cleanup` |
| `pulsecheck_results_deleted_total` | counter | Rows removed by the retention cleanup |
| `pulsecheck_last_round_timestamp_seconds` | gauge | When the last poll round finished (alert if it gets old) |

Plus the standard Go runtime and process metrics.

## Docker

```sh
make docker   # builds pulsecheck-checker from ./Dockerfile
docker run --network host -e DATABASE_URL='postgres://postgres:postgres_dev_password@localhost:5432/postgres?sslmode=disable' pulsecheck-checker
```

`--network host` lets the container reach the dev database on `localhost` (Linux).
Against a database on another host, drop it and pass that `DATABASE_URL`.

## Code layout

```
cmd/checker/main.go       config, poll loop, runs due checks in parallel, cleanup loop
cmd/checker/ops.go        /livez, /healthz, /metrics and the metric definitions
internal/check/           one HTTP check -> Result (no database code)
internal/store/           load due monitors, save results, delete old results (pgx)
```

## Tests

```sh
make test
```

- `internal/check` uses fake HTTP servers: success, server error, expected
  status, `HEAD`, timeout and invalid URL.
- `internal/store` tests which monitors are due (never checked, overdue, recently
  checked, paused) and that deleting old results keeps newer ones, against the
  real database. Skipped when `DATABASE_URL` is not set.
- `cmd/checker` tests `/livez` going unhealthy when rounds stop, and `/metrics`.

## Limits

- Each poll round waits for its slowest check (at most 30s) before the next
  round starts, so one slow site can delay the others by that much.
- One goroutine per due monitor; fine for hundreds of URLs, needs a worker pool
  for many thousands.
- Running two checkers checks every URL twice.
