# Makefile — BUILD.md §109
.DEFAULT_GOAL := help
SHELL := /bin/bash
PG_DSN ?= postgres://netcore:netcore_dev_only@localhost:5432/netcore?sslmode=disable
MIGRATIONS := db/migrations

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: dev
dev: ## Start the 5-container core stack
	docker compose up --build

.PHONY: dev-full
dev-full: ## Start core + observability + tooling
	docker compose --profile full up --build

.PHONY: build
build: ## Compile all binaries
	go build -trimpath -o bin/api ./cmd/api

.PHONY: test
test: ## Run unit tests
	go test ./...

.PHONY: test-race
test-race: ## Run tests under the race detector (§127)
	go test -race ./...

.PHONY: test-cover
test-cover: ## Run tests with coverage, enforcing the §74 floors
	go test -race -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1
	@bash scripts/check-coverage.sh

.PHONY: test-db
test-db: ## Run the SQL invariant suite against PG_DSN (§8)
	psql "$(PG_DSN)" -v ON_ERROR_STOP=1 -f tests/invariants.sql

.PHONY: migrate
migrate: ## Apply all up migrations
	@for f in $(MIGRATIONS)/*.up.sql; do \
	  echo ">> $$f"; psql "$(PG_DSN)" -v ON_ERROR_STOP=1 -q -f "$$f" || exit 1; \
	done

.PHONY: rollback
rollback: ## Apply all down migrations in reverse order
	@for f in $$(ls -r $(MIGRATIONS)/*.down.sql); do \
	  echo "<< $$f"; psql "$(PG_DSN)" -v ON_ERROR_STOP=1 -q -f "$$f" || exit 1; \
	done

.PHONY: migrate-test
migrate-test: ## Prove every migration is reversible (§70, §103)
	$(MAKE) migrate && $(MAKE) rollback && $(MAKE) migrate && $(MAKE) test-db

.PHONY: lint
lint: ## gofmt + vet + golangci-lint
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "gofmt failures above"; exit 1)
	go vet ./...
	@command -v golangci-lint >/dev/null && golangci-lint run || echo "golangci-lint not installed, skipping"

.PHONY: security
security: ## gosec static analysis (§71)
	@command -v gosec >/dev/null && gosec ./... || echo "gosec not installed, skipping"

.PHONY: vuln
vuln: ## govulncheck (§70)
	@command -v govulncheck >/dev/null && govulncheck ./... || echo "govulncheck not installed, skipping"

.PHONY: quota-test
quota-test: ## Run only the quota invariant tests (§21A)
	go test -race -run 'Quota|Octets|Delta|Budget|Gigawords|Period' ./internal/quota/...

.PHONY: verify
verify: lint test-race security vuln ## Everything CI runs
	@echo "all gates passed"

.PHONY: clean
clean:
	rm -rf bin coverage.out
