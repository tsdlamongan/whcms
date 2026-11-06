# WHCMS - the one Makefile of the repo; every day-to-day command lives here.
#
#   make up       start the dev server (backend + frontend)             uses .env
#   make down     stop it: kill the processes, free the ports, clear leftovers
#   make cover    backend tests + coverage (enforces the >=90% gate)    uses .env.testing
#   make lint     lint backend + frontend                               uses .env.testing
#   make format   auto-format backend + frontend
#   make test     lint, then cover, then the e2e suite                  uses .env.testing
#   make help     list the commands
#
# Two env files at the repo root keep development and testing apart:
#   .env          your dev config: real database, SMTP, payment gateway, ...
#                 (copy .env.example, or skip it and use the /install wizard)
#   .env.testing  mocked everything: test database, mockserver, own Redis DB and
#                 S3 bucket. Created from .env.testing.example on first use.
# The process lifecycle (start, kill, wait, clean up) is in scripts/stack.sh.
#
# Needs Go, Node 22 and a local PostgreSQL 18, Redis and RustFS/MinIO (README.md).

SHELL := /bin/bash

DEV_ENV  ?= .env
TEST_ENV ?= .env.testing

# Backend coverage gate (docs/CONTRACTS.md §14). Override: make cover COVERAGE_MIN=95
COVERAGE_MIN ?= 90

# Optional Playwright filter: make e2e SPEC=admin-ops.spec.ts
SPEC ?=

GO_DIRS := backend/cmd backend/internal backend/pkg mockserver

STACK    := bash "$(CURDIR)/scripts/stack.sh"
RUN_TEST := bash "$(CURDIR)/scripts/with-env.sh" $(TEST_ENV)

.DEFAULT_GOAL := help
.PHONY: help up down cover lint format test e2e tunnel hooks guard-e2e-imports

up: | frontend/node_modules ## Start the dev server (api + worker + frontend) with .env
	@$(STACK) up dev $(DEV_ENV)

down: ## Stop the dev server: kill its processes, free the ports, clear cache + leftovers
	@$(STACK) down $(DEV_ENV) $(TEST_ENV)

cover: $(TEST_ENV) ## Run backend tests and show coverage, enforcing the >=90% gate (.env.testing)
	@# Includes the //go:build integration repository tests (docs/CONTRACTS.md §14),
	@# so it needs the test database; test-deps creates + migrates it and starts
	@# the mockserver the adapter integration tests talk to.
	@set -e; \
	trap '$(STACK) stop-mock' EXIT; \
	$(STACK) test-deps $(TEST_ENV); \
	cd backend; \
	echo "==> go build + go vet"; \
	$(RUN_TEST) bash -c 'go build ./... && go vet ./...'; \
	echo "==> go test (unit + integration, race detector)"; \
	$(RUN_TEST) go test -race -covermode=atomic -tags integration -coverprofile=coverage.out ./internal/... ./pkg/...; \
	go tool cover -html=coverage.out -o coverage.html; \
	total="$$(go tool cover -func=coverage.out | awk '/^total:/ {gsub(/%/,"",$$3); print $$3}')"; \
	if [ -z "$$total" ]; then echo "FAIL: could not read total coverage from coverage.out"; exit 1; fi; \
	if awk -v c="$$total" -v min="$(COVERAGE_MIN)" 'BEGIN{exit !(c+0 >= min+0)}'; then \
		echo "PASS: total coverage $$total% meets the >= $(COVERAGE_MIN)% gate"; \
		echo "      HTML report: backend/coverage.html"; \
	else \
		echo "FAIL: total coverage $$total% is below the required $(COVERAGE_MIN)%"; \
		echo "      See uncovered lines: backend/coverage.html"; \
		exit 1; \
	fi

lint: $(TEST_ENV) guard-e2e-imports | frontend/node_modules ## Lint backend + frontend: gofmt, go vet, golangci-lint, prettier, svelte-check (.env.testing)
	@set -e; \
	echo "==> gofmt"; \
	out="$$(gofmt -l $(GO_DIRS))"; \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; echo "(fix with: make format)"; exit 1; fi; \
	echo "==> prettier (frontend)"; \
	(cd frontend && $(RUN_TEST) npm run --silent format:check) || { echo "(fix with: make format)"; exit 1; }; \
	echo "==> go vet (backend, mockserver)"; \
	(cd backend && $(RUN_TEST) go vet ./...); \
	(cd mockserver && $(RUN_TEST) go vet ./...); \
	echo "==> golangci-lint (backend)"; \
	(cd backend && $(RUN_TEST) golangci-lint run ./...); \
	echo "==> svelte-check (frontend)"; \
	(cd frontend && $(RUN_TEST) npm run --silent check); \
	echo "PASS: lint clean"

format: | frontend/node_modules ## Auto-format backend (gofmt) and frontend (prettier)
	gofmt -w $(GO_DIRS)
	cd frontend && npm run --silent format

test: ## Full run: lint, then backend tests + coverage, then the e2e suite (.env.testing)
	@$(MAKE) --no-print-directory lint
	@$(MAKE) --no-print-directory cover
	@$(MAKE) --no-print-directory e2e

e2e: $(TEST_ENV) guard-e2e-imports | frontend/node_modules ## Run only the Playwright suite on a fresh test stack (SPEC=file.spec.ts to filter)
	@# Starts mockserver + api + seed + worker + frontend from .env.testing, runs
	@# Playwright, and always tears the stack down again. The specs hardcode ports
	@# 8080/5173/9090, so a dev server from `make up` is killed to make room.
	@set -e; \
	trap '$(STACK) down $(DEV_ENV) $(TEST_ENV)' EXIT; \
	$(STACK) up testing $(TEST_ENV) $(DEV_ENV); \
	cd frontend; \
	$(RUN_TEST) env CI= npx playwright test $(SPEC)

help: ## List the available commands
	@echo "WHCMS - make targets:"
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-9s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "  .env          dev config used by 'make up'"
	@echo "  .env.testing  test config used by cover / lint / test / e2e"

tunnel: ## Start a cloudflared tunnel to the dev API and list this project's webhooks
	@bash scripts/tunnel.sh

hooks: ## Install the repo git hooks (.githooks; pre-push runs `make lint`)
	git config core.hooksPath .githooks
	@echo "Installed: pre-push now runs 'make lint' (bypass once with SKIP_LINT=1 git push)."

# Fails if a Playwright spec bypasses tests/e2e/fixtures.ts.
guard-e2e-imports:
	@bad="$$(grep -l "from '@playwright/test'" frontend/tests/e2e/*.spec.ts 2>/dev/null || true)"; \
		if [[ -n "$$bad" ]]; then \
			echo "FAIL: specs must import { test, expect } from './fixtures', not '@playwright/test':"; \
			echo "$$bad"; \
			echo "fixtures.ts wraps page.goto to wait for SvelteKit hydration; without it,"; \
			echo "first interactions race hydration on slow machines (green locally, red in CI)."; \
			exit 1; \
		fi
	@echo "==> e2e import guard OK (all specs use ./fixtures)"

# Env file for the test targets: created on first use from the committed
# example, with fresh secrets (never commit generated secrets).
$(TEST_ENV):
	@$(STACK) init-test-env $(TEST_ENV) .env.testing.example

frontend/node_modules:
	cd frontend && npm ci
