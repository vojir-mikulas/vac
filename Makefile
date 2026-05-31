.PHONY: help install dev dev-api dev-ui build build-ui build-api build-api-noembed lint lint-go lint-ui test test-go test-ui test-integration typecheck format clean compose-up compose-down compose-logs

# Version metadata baked into vac-api via -ldflags. Override on the command line
# (e.g. `make build-api VERSION=v1.2.3`) or via env. The release Dockerfile sets
# these from CI build-args; this is the fallback for local builds.
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make <target>\n\nTargets:\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

install: ## Install all dependencies
	pnpm install

dev: ## Run API + UI in parallel (UI dev server proxies /api to :3000)
	@trap 'kill 0' EXIT; \
		$(MAKE) dev-api & \
		$(MAKE) dev-ui & \
		wait

dev-api: ## Run Go API in watch mode (requires `go install github.com/air-verse/air@latest`)
	cd api && go run .

dev-ui: ## Run Vite dev server
	pnpm --filter ui dev

build: build-ui build-api ## Build everything (UI then Go binary with embedded UI)

build-ui: ## Build UI into api/internal/ui/dist
	pnpm --filter ui build

build-api: ## Build Go binary with the embedded UI (requires build-ui first)
	cd api && go build -tags embedui -ldflags="$(LDFLAGS)" -o bin/vac-api .

build-api-noembed: ## Build Go binary without UI (UI not bundled, dev/test)
	cd api && go build -ldflags="$(LDFLAGS)" -o bin/vac-api .

lint: lint-go lint-ui ## Run all linters

lint-go: ## golangci-lint
	cd api && golangci-lint run

lint-ui: ## ESLint + Prettier check
	pnpm --filter ui lint
	pnpm --filter ui format:check

test: test-go test-ui ## Run all unit tests (excludes integration; use test-integration for those)

test-go: ## go test (unit only, race detector on)
	cd api && go test -race ./...

test-integration: ## go test with integration tag (requires Docker, race detector on)
	cd api && go test -race -tags integration ./...

test-ui: ## vitest
	pnpm --filter ui test

typecheck: ## TypeScript typecheck
	pnpm --filter ui typecheck

format: ## Format all code
	cd api && gofmt -w .
	pnpm --filter ui format

clean: ## Remove build artifacts
	rm -rf api/bin api/internal/ui/dist ui/dist ui/node_modules/.vite

compose-up: ## Start vac-db + vac-api via Docker Compose (reads .env)
	docker compose up -d --build

compose-down: ## Stop the compose stack
	docker compose down

compose-logs: ## Tail logs from the compose stack
	docker compose logs -f --tail=100
