GO ?= go
.PHONY: build run migrate test integration vet fmt check test-db-up test-db-down
build:
	$(GO) build -trimpath -o bin/api ./cmd/api
	$(GO) build -trimpath -o bin/migrate ./cmd/migrate
	$(GO) build -trimpath -o bin/adminuser ./cmd/adminuser
	$(GO) build -trimpath -o bin/cohort ./cmd/cohort
run:
	$(GO) run ./cmd/api
migrate:
	$(GO) run ./cmd/migrate
test:
	$(GO) test -race ./internal/... ./test/architecture/...
integration:
	@test -n "$(TEST_DATABASE_URL)" || (echo "TEST_DATABASE_URL is required" >&2; exit 1)
	$(GO) test -race -count=1 ./test/integration/...
vet:
	$(GO) vet ./...
fmt:
	gofmt -w cmd internal api test
check: test vet
	@test -z "$$(gofmt -l cmd internal api test)" || (echo "Run make fmt" >&2; exit 1)
test-db-up:
	docker compose -f deployments/compose.test.yaml up -d --wait
test-db-down:
	docker compose -f deployments/compose.test.yaml down -v
