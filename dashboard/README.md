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
| `/` | All monitors: up / down / pending counts, a form to add a URL, and a table with status, 24h uptime, 24h average latency, last check time and a delete button. |
| `/monitors/{id}` | One monitor: uptime for 24h and 7 days, average latency, a status bar of the last 50 checks, and a table of recent checks with status code, latency and error. |
| `/healthz` | `200 ok` when the API is reachable, otherwise `503`. |

Pages refresh every 30 seconds. They work in light and dark mode and at phone width.

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
internal/web/               routes and handlers
internal/web/templates/     HTML templates, embedded into the binary
```

Uses only the Go standard library. Forms are protected against cross-site
submissions with `http.CrossOriginProtection`.

## Tests

```sh
make test
```

Tests run the handlers against a fake API: the overview page, the detail page,
404s, adding a monitor (including an error from the API), deleting a monitor, the
API being down, and rejecting cross-site form posts.

## Not done yet

No login: anyone who can open the dashboard can add and delete monitors. The
overview makes 2 API calls per monitor, so it gets slower with hundreds of
monitors; a summary endpoint in the API would fix that.
