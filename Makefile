.PHONY: build test lint run docker clean

build:
	go build -o bin/router .

test:
	go test ./... -v -race -count=1

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:" && gofmt -l . && exit 1)

run: build
	./bin/router

docker:
	docker compose up --build

clean:
	rm -rf bin/
