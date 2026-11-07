# Build from this directory: docker build -t pulsecheck-checker .
FROM golang:1.26-alpine AS build
WORKDIR /src
# Download modules first so this layer is cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/checker ./cmd/checker

# Static base: CA certificates and tzdata included, no shell, runs as non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/checker /checker
ENTRYPOINT ["/checker"]
