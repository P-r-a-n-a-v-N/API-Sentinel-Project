# API Sentinel — Makefile
# Convenient shortcuts for common development tasks.

.PHONY: all build test test-race lint cover run-backend run-gateway clean docker-up docker-down

## Primary targets ─────────────────────────────────────────────────────────────

all: build

## Build the gateway binary into ./bin/
build:
	@mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/gateway ./cmd/gateway/...
	@echo "✓  Built: bin/gateway"

## Run all tests (excludes node_modules and dashboard vendor code)
PKGS := $(shell go list ./... | grep -v node_modules | grep -v dashboard)

test:
	REDIS_ADDR=localhost:6379 go test -timeout 60s $(PKGS)

## Run tests with race detector (required before every PR)
test-race:
	REDIS_ADDR=localhost:6379 go test -race -timeout 60s $(PKGS)

## Generate HTML coverage report and open it
cover:
	REDIS_ADDR=localhost:6379 go test -coverprofile=coverage.out -covermode=atomic $(PKGS)
	go tool cover -html=coverage.out -o coverage.html
	@echo "✓  Coverage report: coverage.html"
	@go tool cover -func=coverage.out | tail -1

## Run golangci-lint
lint:
	golangci-lint run ./...

## Run go vet
vet:
	go vet $(PKGS)

## Local development: start dummy backend + gateway ────────────────────────────

## Start the dummy upstream backend on :9000
run-backend:
	go run ./scripts/dummy_backend.go

## Start the gateway on :8080 pointing at the dummy backend
run-gateway:
	GATEWAY_PORT=8080 \
	UPSTREAM_URL=http://localhost:9000 \
	REDIS_ADDR=localhost:6379 \
	LOG_LEVEL=debug \
	go run ./cmd/gateway/main.go

## Start the React dashboard dev server on :3000
run-dashboard:
	cd dashboard && npm run dev

## Docker ──────────────────────────────────────────────────────────────────────

## Build and start the full stack (Redis + gateway + dummy backend + dashboard)
docker-up:
	docker-compose up --build

## Start stack in background
docker-up-d:
	docker-compose up --build -d

## Stop and remove containers and volumes
docker-down:
	docker-compose down -v

## Clean build artifacts ───────────────────────────────────────────────────────
clean:
	rm -rf bin/ coverage.out coverage.html
	cd dashboard && rm -rf dist/
