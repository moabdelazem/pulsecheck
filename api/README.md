# Pulse Check - API

HTTP API for managing monitors and reading their check results. It reads and writes
the Postgres database that the [checker](../checker/) fills with results.

## Run

```sh
../scripts/dev/database.sh   # once: start Postgres and create the tables
make run                     # listens on :8080
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | dev database (set by the Makefile) | Postgres connection string. Required when running the binary directly. |
| `ADDR` | `:8080` | Address to listen on. |

## Endpoints

Request and response bodies are JSON. Errors look like `{"error": "message"}`.

| Method | Path | Description |
|---|---|---|
| `GET` | `/healthz` | `200` if the database is reachable, else `503` |
| `GET` | `/monitors` | List all monitors |
| `POST` | `/monitors` | Create a monitor. Body: `{"url": "https://example.com"}` |
| `GET` | `/monitors/{id}` | Get one monitor |
| `PUT` | `/monitors/{id}` | Change a monitor's URL. Body: `{"url": "..."}` |
| `DELETE` | `/monitors/{id}` | Delete a monitor and all its results |
| `GET` | `/monitors/{id}/results?limit=50` | Latest results, newest first. `limit` 1–500, default 50 |
| `GET` | `/monitors/{id}/stats?window=24h` | Checks, uptime % and average latency over a window (Go duration, max `2160h`) |

URLs must be absolute `http` or `https` URLs, at most 2048 characters.

Status codes: `400` invalid input, `404` monitor not found, `409` URL already
monitored, `500` unexpected error (details go to the log, not the response).

## Examples

```sh
curl -X POST localhost:8080/monitors -d '{"url": "https://go.dev"}'
# {"id":1,"url":"https://go.dev","created_at":"2026-11-07T19:55:51+02:00"}

curl localhost:8080/monitors/1/results?limit=1
# [{"id":10,"up":true,"status_code":200,"latency_ms":238,"error":null,"checked_at":"..."}]

curl localhost:8080/monitors/1/stats?window=1h
# {"stats":{"checks":2,"up_checks":2,"uptime_percent":100,"avg_latency_ms":444},"window":"1h0m0s"}
```

`status_code` is `null` when the site never answered (timeout, DNS error, ...);
`error` then says why.

## Code layout

```
cmd/api/main.go           config, database pool, HTTP server, graceful shutdown
internal/server/          routes, handlers, input validation
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
request logging, CORS, or pagination for `GET /monitors`.
