APP := api
BIN := bin/$(APP)

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
