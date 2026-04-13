#!/usr/bin/env bash
set -euo pipefail

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}"
BUILD_TIME="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
LDFLAGS="-ldflags=-X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}"

build() {
    go build "${LDFLAGS}" -o bin/router ./cmd/router
}

test_all() {
    go test ./... -v -race -count=1
}

bench() {
    go test ./internal/classifier -bench=. -benchmem
    go test ./internal/scorer -bench=. -benchmem
}

lint() {
    go vet ./...
    if [[ -n "$(gofmt -l .)" ]]; then
        echo "gofmt needed on:"
        gofmt -l .
        exit 1
    fi
}

run() {
    build
    ./bin/router
}

docker_up() {
    docker compose up --build
}

clean() {
    rm -rf bin/
}

case "${1:-}" in
    build)   build ;;
    test)    test_all ;;
    bench)   bench ;;
    lint)    lint ;;
    run)     run ;;
    docker)  docker_up ;;
    clean)   clean ;;
    *)
        echo "Usage: $0 {build|test|bench|lint|run|docker|clean}"
        exit 1
        ;;
esac