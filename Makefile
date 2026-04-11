VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)"

.PHONY: build test bench lint run docker clean

build:
	go build $(LDFLAGS) -o bin/router ./cmd/router

test:
	go test ./... -v -race -count=1

bench:
	go test ./internal/classifier -bench=. -benchmem
	go test ./internal/scorer -bench=. -benchmem

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:" && gofmt -l . && exit 1)

run: build
	./bin/router

docker:
	docker compose up --build

clean:
	rm -rf bin/
