.PHONY: help install dev dev-api dev-ui build build-ui build-api lint lint-go lint-ui test test-go test-ui typecheck format clean

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

build-api: ## Build Go binary (expects UI build to exist)
	cd api && go build -ldflags="-s -w" -o bin/vac-api .

lint: lint-go lint-ui ## Run all linters

lint-go: ## golangci-lint
	cd api && golangci-lint run

lint-ui: ## ESLint + Prettier check
	pnpm --filter ui lint
	pnpm --filter ui format:check

test: test-go test-ui ## Run all tests

test-go: ## go test
	cd api && go test ./...

test-ui: ## vitest
	pnpm --filter ui test

typecheck: ## TypeScript typecheck
	pnpm --filter ui typecheck

format: ## Format all code
	cd api && gofmt -w .
	pnpm --filter ui format

clean: ## Remove build artifacts
	rm -rf api/bin api/internal/ui/dist ui/dist ui/node_modules/.vite
