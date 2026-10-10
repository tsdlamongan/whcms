SHELL := /bin/bash

# Load .env when present (local dev convenience).
ifneq (,$(wildcard .env))
include .env
export
endif

DATABASE_URL ?= postgres://root:postgres@localhost:5432/whmcs_e2e?sslmode=disable

.PHONY: build test test-integration cover cover-gate guard-test-db run-api run-worker run-cli migrate-up migrate-down vet fmt lint

build:
	go build ./...

vet:
	go vet ./...

## lint: golangci-lint (config: .golangci.yml). Install:
##   go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
lint:
	golangci-lint run ./...

fmt:
	gofmt -w cmd internal pkg migrations

test:
	go test -race -covermode=atomic ./...

## guard-test-db: refuses to run integration tests against a DATABASE_URL that
## doesn't look like a dedicated test/e2e database. Integration tests CREATE
## and DELETE real rows (product_groups/products/servers/clients/orders/...),
## and the `include .env` above means running `test-integration`/`cover-gate`
## directly from backend/ silently picks up your own dev DATABASE_URL (from
## backend/.env) instead of the isolated whmcs_e2e one — this has actually
## polluted a real database before. Always prefer `make test-backend` from
## the repo root, which forces DATABASE_URL explicitly (command-line
## overrides always win over `.env`/this file's default, so it passes here).
guard-test-db:
	@case "$(DATABASE_URL)" in \
		*_e2e*|*_test*) ;; \
		*) \
			echo "REFUSING: DATABASE_URL does not look like a dedicated test database:"; \
			echo "  $(DATABASE_URL)"; \
			echo "Integration tests create and delete real rows — running them against a"; \
			echo "non-test database (e.g. your own backend/.env dev DB) will pollute it."; \
			echo "Run 'make test-backend' from the repo root instead (it forces the"; \
			echo "isolated whmcs_e2e database), or pass DATABASE_URL= explicitly if this"; \
			echo "really is a disposable database."; \
			exit 1 ;; \
	esac

## test-integration: also runs //go:build integration repo tests (needs local PG with migrations applied)
test-integration: guard-test-db
	go test -race -tags integration ./...

cover:
	go test -race -covermode=atomic -coverprofile=coverage.out ./internal/... ./pkg/...
	go tool cover -func=coverage.out | tail -1
	go tool cover -html=coverage.out -o coverage.html

## cover-gate: the >90% quality gate (CONTRACTS §14). Includes the //go:build
## integration repository tests, so it needs a running + migrated Postgres
## (DATABASE_URL). This is what reaches the ~95% total; plain `cover` is unit-only.
cover-gate: guard-test-db
	go test -race -covermode=atomic -tags integration -coverprofile=coverage.out ./internal/... ./pkg/...
	go tool cover -func=coverage.out | tail -1
	go tool cover -html=coverage.out -o coverage.html

run-api:
	go run ./cmd/api

run-worker:
	go run ./cmd/worker

run-cli:
	go run ./cmd/cli

build-cli:
	go build -o ../whcms ./cmd/cli

# Migrations run automatically on API start; these targets are for manual ops.
migrate-up:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down 1
