# Pulse Check

A small uptime monitor written in Go. You register URLs through the API, the checker
calls them on a timer, and every result is stored in Postgres so you can see uptime
and latency over time.

## How it fits together

```
           POST /monitors                          GET each URL
  you ───────────────────▶  api  ──┐      ┌──  checker  ───────────▶  monitored sites
           GET results/stats       │      │
                                   ▼      ▼
                                  Postgres
                          monitors · check_results
```

The two services never talk to each other directly; they share the database.

| Part | What it does | Docs |
|---|---|---|
| [`api/`](api/) | HTTP API to manage monitors and read results and stats | [api/README.md](api/README.md) |
| [`checker/`](checker/) | Checks every monitor on an interval and saves the results | [checker/README.md](checker/README.md) |
| [`scripts/dev/`](scripts/dev/) | Dev Postgres container and the database schema | below |

## Quick start

Requirements: Go 1.26+, Docker.

```sh
# 1. Start Postgres and create the tables
./scripts/dev/database.sh

# 2. Start the API (terminal 1)
cd api && make run

# 3. Start the checker (terminal 2)
cd checker && make run

# 4. Add something to monitor
curl -X POST localhost:8080/monitors -d '{"url": "https://go.dev"}'

# 5. A few seconds later, see the results
curl localhost:8080/monitors/1/results
curl localhost:8080/monitors/1/stats
```

## Database

`scripts/dev/database.sh` starts a `postgres:18.6` container called
`pulsecheck_database_dev` on port `5432`, waits until it is ready, and applies
[`scripts/dev/schema.sql`](scripts/dev/schema.sql). Data lives in the
`pulsecheck_postgres_dev` Docker volume, so it survives re-running the script.

The schema is safe to apply again at any time (`CREATE ... IF NOT EXISTS`):

- `monitors`: the URLs to check (`url` is unique).
- `check_results`: one row per check (up/down, status code, latency, error, time).
  Deleting a monitor deletes its results.

Both apps default to the dev database in their Makefiles. Point them elsewhere with
`DATABASE_URL`.

## Development

Each app is its own Go module with the same Makefile targets:

```sh
make run     # run the app
make build   # build to bin/<app>
make test    # run tests (api tests use the dev database)
make fmt     # go fmt
make vet     # go vet
make tidy    # go mod tidy
make clean   # remove bin/
make docker  # build the Docker image pulsecheck-<app>
```

## Docker

Each app has a multi-stage `Dockerfile`: a static Go binary on a distroless,
non-root base image (~21 MB). Build from inside the app folder so its
`.dockerignore` applies:

```sh
cd api && make docker        # -> pulsecheck-api
cd checker && make docker    # -> pulsecheck-checker
```

Run them against the dev database. Inside a container `localhost` is the container
itself, so use the host network (Linux):

```sh
DB='postgres://postgres:postgres_dev_password@localhost:5432/postgres?sslmode=disable'
docker run --network host -e DATABASE_URL="$DB" pulsecheck-api
docker run --network host -e DATABASE_URL="$DB" pulsecheck-checker
```

## Status

Work in progress. Not done yet: authentication, docker compose, per-monitor check
intervals, cleanup of old results, and running more than one checker safely.
