# Pulse Check - Dashboard

Web dashboard for Pulse Check. See every monitor's status, uptime and latency, look
at recent checks, and add or delete monitors. It has no database access: everything
goes through the [API](../api/).

## Run

```sh
# The API must be running (see ../api)
make run      # http://localhost:3000
```

## Configuration

| Variable | Default | Description |
|---|---|---|
| `API_URL` | `http://localhost:8080` | Base URL of the Pulse Check API. |
| `ADDR` | `:3000` | Address to listen on. |

## Pages

| Path | What you see |
|---|---|
| `/` | All monitors: up / down / pending / paused counts, a form to add a URL (with an optional name and check interval), and a table with status, name, interval, 24h uptime, 24h average latency, last check time, and pause/resume and delete buttons. |
| `/monitors/{id}` | One monitor: its settings and status, uptime for 24h and 7 days, a response-time chart (1h / 24h / 7d), a status bar of the last 50 checks, a settings form (name, method, interval, timeout, expected status), pause/resume, and a table of recent checks. |
| `/livez` | `200 ok` while the process is serving. |
| `/healthz` | `200 ok` when the API is reachable, otherwise `503`. |
| `/metrics` | Prometheus metrics: `http_requests_total{route, code}`, `http_request_duration_seconds{route}`, Go runtime and process metrics. |

Pages refresh every 30 seconds. They work in light and dark mode and at phone width.

The response-time chart shows the average latency of successful checks per time
slot (1 minute, 15 minutes or 2 hours, depending on the range). Slots with at least
one failed check are shaded red, gaps in the data break the line, and hovering a
slot shows its numbers. It is drawn as plain SVG on the server, with no JavaScript.

If the API is down, pages show a message instead of failing and keep retrying.
Errors from the API about bad input, such as a duplicate or invalid URL, are shown
on the page as they are.

## Docker

```sh
make docker   # builds pulsecheck-dashboard from ./Dockerfile
docker run --network host -e API_URL=http://localhost:8080 pulsecheck-dashboard
```

`--network host` lets the container reach an API running on `localhost` (Linux).
Otherwise publish the port with `-p 3000:3000` and point `API_URL` at the API.

## Code layout

```
cmd/dashboard/main.go       config, HTTP server, graceful shutdown
internal/apiclient/         typed client for the Pulse Check API
internal/web/               routes, handlers, chart, metrics middleware
internal/web/templates/     HTML templates, embedded into the binary
```

Uses the Go standard library plus the Prometheus client for `/metrics`. Forms are
protected against cross-site submissions with `http.CrossOriginProtection`.

## Tests

```sh
make test
```

Tests run the handlers against a fake API: the overview and detail pages (every
chart range), 404s, adding a monitor, saving settings, pause and resume, deleting,
errors from the API, the API being down, and rejecting cross-site form posts. The
chart geometry (gaps, failure bands, axis rounding) has its own tests.

## Not done yet

No login: anyone who can open the dashboard can add and delete monitors. The
overview makes 2 API calls per monitor, so it gets slower with hundreds of
monitors; a summary endpoint in the API would fix that.
