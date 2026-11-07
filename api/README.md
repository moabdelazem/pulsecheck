# Pulse Check - API

HTTP API for managing monitors and reading their check results. It reads and writes
the Postgres database that the [checker](../checker/) fills with results.

## Run

```sh
../scripts/dev/database.sh   # once: start Postgres and create the tables
make run                     # listens on :8080
```

## Docker

```sh
make docker   # builds pulsecheck-api from ./Dockerfile
docker run --network host -e DATABASE_URL='postgres://postgres:postgres_dev_password@localhost:5432/postgres?sslmode=disable' pulsecheck-api
```

`--network host` lets the container reach the dev database on `localhost` (Linux).
Against a database on another host, drop it and pass that `DATABASE_URL` and `-p 8080:8080`.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | dev database (set by the Makefile) | Postgres connection string. Required when running the binary directly. |
| `ADDR` | `:8080` | Address to listen on. |

## Endpoints

Request and response bodies are JSON. Errors look like `{"error": "message"}`.

| Method | Path | Description |
|---|---|---|
| `GET` | `/livez` | `200` while the process is serving (does not check the database) |
| `GET` | `/healthz` | `200` if the database is reachable, else `503` |
| `GET` | `/metrics` | Prometheus metrics (text format, not JSON) |
| `GET` | `/monitors` | List all monitors |
| `POST` | `/monitors` | Create a monitor. Only `url` is required; see [Monitor fields](#monitor-fields) |
| `GET` | `/monitors/{id}` | Get one monitor |
| `PATCH` | `/monitors/{id}` | Change any monitor fields; fields you leave out keep their value |
| `DELETE` | `/monitors/{id}` | Delete a monitor and all its results |
| `GET` | `/monitors/{id}/results?limit=50` | Latest results, newest first. `limit` 1–500, default 50 |
| `GET` | `/monitors/{id}/stats?window=24h` | Checks, uptime % and average latency over a window (Go duration, max `2160h`) |
| `GET` | `/monitors/{id}/latency?window=24h&bucket=15m` | Checks grouped into time slots for charts: per slot the number of checks, failures and average latency of successful checks. `bucket` at least `10s`, at most 1000 slots |

## Monitor fields

| Field | Default | Rules |
|---|---|---|
| `url` | required | Absolute `http` or `https` URL, at most 2048 characters, unique |
| `name` | `""` | Display name, at most 100 characters |
| `method` | `"GET"` | `"GET"` or `"HEAD"` |
| `interval_seconds` | `30` | How often to check, 10–86400 |
| `timeout_seconds` | `5` | Request timeout, 1–30 |
| `expected_status` | `null` | Exact status code that counts as up (100–599). `null` = any 2xx or 3xx. Redirects are followed, so it is compared with the final response |
| `paused` | `false` | Paused monitors are not checked; their history is kept |

The same limits are enforced by the database (`scripts/dev/schema.sql`).

Status codes: `400` invalid input, `404` monitor not found, `409` URL already
monitored, `500` unexpected error (details go to the log, not the response).

## Examples

```sh
curl -X POST localhost:8080/monitors -d '{"url": "https://go.dev", "name": "Go website"}'
# {"id":1,"url":"https://go.dev","name":"Go website","method":"GET","interval_seconds":30,
#  "timeout_seconds":5,"expected_status":null,"paused":false,"created_at":"..."}

curl -X PATCH localhost:8080/monitors/1 -d '{"interval_seconds": 60, "expected_status": 200}'
curl -X PATCH localhost:8080/monitors/1 -d '{"paused": true}'           # pause
curl -X PATCH localhost:8080/monitors/1 -d '{"expected_status": null}'  # any 2xx/3xx again

curl localhost:8080/monitors/1/results?limit=1
# [{"id":10,"up":true,"status_code":200,"latency_ms":238,"error":null,"checked_at":"..."}]

curl localhost:8080/monitors/1/stats?window=1h
# {"stats":{"checks":2,"up_checks":2,"uptime_percent":100,"avg_latency_ms":444},"window":"1h0m0s"}

curl "localhost:8080/monitors/1/latency?window=1h&bucket=1m"
# {"bucket":"1m0s","window":"1h0m0s","buckets":[{"time":"...","checks":2,"down":0,"avg_latency_ms":231.5}, ...]}
```

`status_code` is `null` when the site never answered (timeout, DNS error, ...);
`error` then says why.

## Health and metrics

Every app serves the same three endpoints:

| Endpoint | Meaning | Use it for |
|---|---|---|
| `/livez` | The process is running (the checker also fails it if its poll loop stalls) | Liveness probe: restart on failure |
| `/healthz` | `/livez` plus its dependency (Postgres) is reachable | Readiness probe: stop sending traffic on failure |
| `/metrics` | Prometheus metrics | Scraping |

Metrics: `http_requests_total{route, code}` and `http_request_duration_seconds{route}`,
plus the standard Go runtime and process metrics. `route` is the route pattern such
as `GET /monitors/{id}`, so monitor IDs never become labels.

## Code layout

```
cmd/api/main.go           config, database pool, HTTP server, graceful shutdown
internal/server/          routes, handlers, input validation, metrics middleware
internal/store/           SQL queries (pgx)
```

## Tests

```sh
make test
```

`TestAPI` runs the whole monitor lifecycle against the real database and cleans up
after itself. It is skipped when `DATABASE_URL` is not set (plain `go test`).

## Not done yet

No authentication: anyone who can reach the API can change monitors. Also no
request logging, CORS, or pagination for `GET /monitors`. `/metrics` is public on
the same port as the API. Two `PATCH` requests
to the same monitor at the same moment can overwrite each other.
