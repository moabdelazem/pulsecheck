APP := api
BIN := bin/$(APP)

# Dev database from scripts/dev/database.sh; override with DATABASE_URL=...
DATABASE_URL ?= postgres://postgres:postgres_dev_password@localhost:5432/postgres?sslmode=disable
export DATABASE_URL

.PHONY: run build test fmt vet tidy clean

run:
	go run ./cmd/$(APP)

build:
	go build -o $(BIN) ./cmd/$(APP)

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf bin
