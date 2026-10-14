.PHONY: build test migrate run
build:
	go build -o bin/api ./cmd/api
	go build -o bin/migrate ./cmd/migrate
test:
	go test -race ./...
migrate:
	go run ./cmd/migrate
run:
	go run ./cmd/api
