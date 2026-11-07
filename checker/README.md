# Pulse Check - Checker

Background worker that checks every monitor on a fixed interval and stores each
result in Postgres. Monitors are managed through the [API](../api/).

## How it works

Every `CHECK_INTERVAL` (and once right at startup) the checker:

1. Loads all rows from `monitors`.
2. Sends a `GET` to every URL in parallel, with `CHECK_TIMEOUT` per request.
3. Saves one row per URL to `check_results`.

A check counts as **up** when the site answers with a `2xx` or `3xx` status.
Timeouts, DNS errors, refused connections and `4xx`/`5xx` responses count as
**down**. On `Ctrl+C` / `SIGTERM` it stops cleanly and does not save checks that
were cut short.

## Run

```sh
../scripts/dev/database.sh   # once: start Postgres and create the tables
make run
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | dev database (set by the Makefile) | Postgres connection string. Required when running the binary directly. |
| `CHECK_INTERVAL` | `30s` | Time between rounds of checks (Go duration, e.g. `10s`, `1m`). |
| `CHECK_TIMEOUT` | `5s` | Maximum time for one request. |

Example: `CHECK_INTERVAL=10s make run`

## Code layout

```
cmd/checker/main.go       config, schedule, runs checks in parallel
internal/check/           one HTTP check -> Result (no database code)
internal/store/           load monitors, save results (pgx)
```

## Tests

```sh
make test
```

`internal/check` is tested with fake HTTP servers: success, server error, timeout
and invalid URL.

## Limits

- All monitors share one interval.
- One goroutine per monitor per round; fine for hundreds of URLs, needs a worker
  pool for many thousands.
- Running two checkers checks every URL twice.
