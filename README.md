# Pulse Check

A small uptime monitor written in Go, made of three apps. You add URLs in the
dashboard (or through the API), the checker calls them on a timer, and every result
is stored in Postgres so you can see uptime and latency over time.

## How it fits together

```
  browser ──▶ dashboard ──HTTP──▶ api ──┐      ┌── checker ──GET each URL──▶ monitored sites
                                        ▼      ▼
                                       Postgres
                               monitors · check_results
```

Only the API and the checker use the database. The dashboard reads and changes data
through the API. Each app is its own Go module and Docker image, so each can be
built and deployed on its own.

| Part | What it does | Docs |
|---|---|---|
| [`api/`](api/) | HTTP API to manage monitors and read results and stats | [api/README.md](api/README.md) |
| [`checker/`](checker/) | Checks every monitor on an interval and saves the results | [checker/README.md](checker/README.md) |
| [`dashboard/`](dashboard/) | Web UI: status, uptime, latency, recent checks; add and delete monitors | [dashboard/README.md](dashboard/README.md) |
| [`scripts/dev/`](scripts/dev/) | Dev Postgres container and the database schema | below |

## Quick start (Docker Compose)

Requirements: Docker.

```sh
cp .env.example .env          # once; database settings live here
docker compose up --build     # postgres, schema, api, checker, dashboard
```

| Service | URL |
|---|---|
| Dashboard | http://localhost:13000 |
| API | http://localhost:18080 |
| Postgres | `localhost:15432` |

The ports are fixed in [`compose.yml`](compose.yml) and chosen to avoid common
defaults. The `migrate` service applies [`scripts/dev/schema.sql`](scripts/dev/schema.sql)
on every start, then exits. Data is kept in the `pulsecheck_pgdata` volume;
`docker compose down -v` deletes it.

## Quick start (without Docker for the apps)

Requirements: Go 1.26+, Docker.

```sh
# 1. Start Postgres and create the tables
./scripts/dev/database.sh

# 2. Start the API (terminal 1)
cd api && make run

# 3. Start the checker (terminal 2)
cd checker && make run

# 4. Start the dashboard (terminal 3)
cd dashboard && make run

# 5. Open http://localhost:3000 and add a URL to monitor
```

Or use the API directly:

```sh
curl -X POST localhost:8080/monitors -d '{"url": "https://go.dev"}'
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

The API and the checker default to the dev database in their Makefiles. Point them elsewhere with
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
cd dashboard && make docker  # -> pulsecheck-dashboard
```

Run them against the dev database. Inside a container `localhost` is the container
itself, so use the host network (Linux):

```sh
DB='postgres://postgres:postgres_dev_password@localhost:5432/postgres?sslmode=disable'
docker run --network host -e DATABASE_URL="$DB" pulsecheck-api
docker run --network host -e DATABASE_URL="$DB" pulsecheck-checker
docker run --network host -e API_URL=http://localhost:8080 pulsecheck-dashboard
```

## Status

Work in progress. Not done yet: authentication (API and dashboard), Kubernetes
manifests for GitOps, docker compose, per-monitor check intervals, cleanup of old
results, and running more than one checker safely.
