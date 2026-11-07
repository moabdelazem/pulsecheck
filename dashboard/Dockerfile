# Build from this directory: docker build -t pulsecheck-dashboard .
FROM golang:1.26-alpine AS build
WORKDIR /src
# Standard library only, so there is no go.sum and nothing to download.
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dashboard ./cmd/dashboard

# Static base: CA certificates and tzdata included, no shell, runs as non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/dashboard /dashboard
EXPOSE 3000
ENTRYPOINT ["/dashboard"]
