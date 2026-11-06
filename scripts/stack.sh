#!/usr/bin/env bash
# Process lifecycle for the local stacks, driven by the root Makefile.
#
#   stack.sh up dev ENV_FILE        dev server: api + worker + frontend, with the
#                                   real integrations configured in ENV_FILE (.env)
#   stack.sh up testing ENV_FILE    e2e stack: mockserver + api + seed + worker +
#                                   frontend, all mocked (.env.testing)
#   stack.sh down [DEV_ENV [TEST_ENV]]
#                                   stop every process, free the ports, clean up
#   stack.sh test-deps ENV_FILE     for `make cover`: check infra, create + migrate
#                                   the test DB, start the mockserver on :9090
#   stack.sh stop-mock              stop the mockserver (and only that)
#   stack.sh init-test-env FILE EXAMPLE
#                                   create FILE from EXAMPLE with fresh secrets
#
# State (pid files, logs, built binaries) lives in <repo>/.stack/. The dev and
# testing stacks share ports (the Playwright specs hardcode 8080/5173/9090), so
# only one runs at a time and starting either one kills whatever holds them.
set -euo pipefail

# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# ---------------------------------------------------------------------------
# Building and starting processes
# ---------------------------------------------------------------------------

# start_bg NAME DIR CMD...: run CMD in DIR detached from this script, logging
# to .stack/NAME.log. `exec` keeps the recorded pid the real process's, not a
# wrapper subshell's.
start_bg() {
	local name="$1" dir="$2"
	shift 2
	mkdir -p "$STATE_DIR"
	(cd "$dir" && exec nohup "$@") </dev/null >"$STATE_DIR/$name.log" 2>&1 &
	write_pidfile "$name" "$!"
}

# Binaries are built to .stack/bin and run directly, never via `go run`: that
# execs the compiled binary as a child, so a pidfile would hold the wrapper's
# pid and the real process (a worker binds no port) would leak on teardown.
build_backend() {
	local name
	mkdir -p "$BIN_DIR"
	for name in "$@"; do
		log "building $name"
		(cd "$REPO_ROOT/backend" && go build -o "$BIN_DIR/$name" "./cmd/$name")
	done
}

build_mockserver() {
	mkdir -p "$BIN_DIR"
	log "building mockserver"
	(cd "$REPO_ROOT/mockserver" && go build -o "$BIN_DIR/mockserver" .)
}

start_mockserver() {
	local port="${MOCK_PORT:-9090}"
	build_mockserver
	export MOCK_PORT="$port"
	log "starting mockserver on :$port"
	start_bg mockserver "$REPO_ROOT/mockserver" "$BIN_DIR/mockserver"
	wait_http "http://localhost:$port/healthz" mockserver 30
}

# start_backend_dev runs api or worker under air (live reload) when it's
# installed; air's CLI flags override .air.toml, so that file stays the one
# `air` uses by hand.
start_backend_dev() {
	local name="$1"
	if command -v air >/dev/null 2>&1; then
		mkdir -p "$BIN_DIR"
		log "starting $name (air, live reload)"
		start_bg "$name" "$REPO_ROOT/backend" air -c .air.toml --tmp_dir "$STATE_DIR/air/$name" \
			--build.cmd "go build -o $BIN_DIR/$name ./cmd/$name" --build.bin "$BIN_DIR/$name"
	else
		warn "air is not installed - $name runs without live reload (restart with 'make up' to pick up changes)"
		build_backend "$name"
		log "starting $name"
		start_bg "$name" "$REPO_ROOT/backend" "$BIN_DIR/$name"
	fi
}

# wait_worker waits for the worker to log that it is running; a worker that hit
# a config error exits at once and never logs it.
wait_worker() {
	local tries="$1" i
	for ((i = 0; i < tries; i++)); do
		if grep -q "worker running" "$STATE_DIR/worker.log" 2>/dev/null; then
			log "worker is up"
			return 0
		fi
		sleep 1
	done
	warn "worker did not log 'worker running' - last lines of $STATE_DIR/worker.log:"
	tail -n 20 "$STATE_DIR/worker.log" >&2 || true
	return 1
}

# start_frontend launches vite with a scrubbed environment: only what the
# SvelteKit BFF needs (API_URL, PUBLIC_*), never the backend's secrets (JWT key,
# DB password, SMTP password, ...) that the loaded env file carries.
start_frontend() {
	local port="$1" k
	local -a fenv=()
	[[ -d "$REPO_ROOT/frontend/node_modules" ]] || {
		log "frontend/node_modules missing - running npm ci"
		(cd "$REPO_ROOT/frontend" && npm ci)
	}
	for k in PATH HOME USER SHELL TERM LANG LC_ALL TMPDIR NODE_OPTIONS API_URL ORIGIN $(compgen -e | grep '^PUBLIC_' || true); do
		if [[ -n "${!k:-}" ]]; then fenv+=("$k=${!k}"); fi
	done
	log "starting frontend on :$port"
	start_bg frontend "$REPO_ROOT/frontend" env -i "${fenv[@]}" npm run dev -- --port "$port"
	wait_http "http://localhost:$port/" frontend 90 1
}

# ---------------------------------------------------------------------------
# Teardown
# ---------------------------------------------------------------------------

# stop_stack stops everything this repo started and frees the given ports.
stop_stack() {
	local name port
	for name in frontend worker api mockserver; do stop_pidfile "$name"; done
	sweep_strays
	for port in "$@"; do free_port "$port"; done
}

# clean_redis cleans the Redis DB of the loaded env. Only key patterns, never
# FLUSHDB: that Redis instance is shared with other projects.
#   dev     the app's cache and locks (a killed process can leave a lock held)
#   testing everything the stack wrote (queues, sessions, rate limits, cache),
#           so each e2e run starts from a clean slate
clean_redis() {
	case "$1" in
	dev)
		redis_del_pattern 'whmcs:cache:*'
		redis_del_pattern 'whmcs:lock:*'
		;;
	testing)
		redis_del_pattern 'whmcs:*'
		redis_del_pattern 'asynq:*'
		;;
	esac
}

# redis_id names the Redis DB an env file points at.
redis_id() {
	printf '%s/%s' "$(env_get "$1" REDIS_ADDR)" "$(env_get "$1" REDIS_DB)"
}

stack_ports() { # ENV_FILE -> "api_port frontend_port" it uses
	printf '%s %s' "$(env_get "$1" APP_PORT)" "$(url_port "$(env_get "$1" FRONTEND_URL)" 5173)"
}

# ---------------------------------------------------------------------------
# Commands
# ---------------------------------------------------------------------------

cmd_up_dev() {
	local file api_port fe_port
	file="$(abs_path "$1")"
	if [[ -f "$file" ]]; then
		load_env "$file"
		preflight_infra
	else
		warn "$file not found - the API will start in its installation wizard (open /install)"
	fi
	export ENV_FILE="$file"
	api_port="${APP_PORT:-8080}"
	fe_port="$(url_port "${FRONTEND_URL:-http://localhost:5173}" 5173)"
	export API_URL="${API_URL:-http://localhost:$api_port}"

	log "tearing down any existing stack first"
	stop_stack "$api_port" "$fe_port"
	if [[ -f "$file" ]]; then clean_redis dev; fi

	start_backend_dev api
	wait_http "http://localhost:$api_port/healthz" api 180 1
	if [[ -f "$file" ]]; then
		start_backend_dev worker
		# air builds first; don't fail `make up` over a worker with a bad config
		wait_worker 120 || warn "no worker running (queued emails/provisioning won't be processed) - fix the config, then run 'make up' again"
	else
		warn "no worker yet: finish the wizard at http://localhost:$api_port/install (it writes $file), then run 'make up' again to start the worker"
	fi
	start_frontend "$fe_port"

	log "dev stack is up (config: $file)"
	echo "  api      http://localhost:$api_port"
	echo "  frontend http://localhost:$fe_port"
	echo "  logs     $STATE_DIR/{api,worker,frontend}.log"
	echo "  stop     make down"
}

cmd_up_testing() {
	local file api_port fe_port mock_port dev_env="${2:-}"
	file="$(abs_path "$1")"
	[[ -f "$file" ]] || die "$file not found"
	load_env "$file"
	export ENV_FILE="$file"
	guard_test_db
	preflight_infra
	api_port="${APP_PORT:-8080}"
	fe_port="$(url_port "${FRONTEND_URL:-http://localhost:5173}" 5173)"
	mock_port="${MOCK_PORT:-9090}"
	export API_URL="${API_URL:-http://localhost:$api_port}"

	log "tearing down any existing stack first for a clean, isolated run"
	stop_stack "$api_port" "$fe_port" "$mock_port"
	if [[ -n "$dev_env" && -f "$(abs_path "$dev_env")" && "$(redis_id "$(abs_path "$dev_env")")" == "$(redis_id "$file")" ]]; then
		warn "$file and $dev_env use the same Redis DB - not wiping it, but dev and test would share queues"
	else
		clean_redis testing
	fi
	ensure_database "$DATABASE_URL"

	build_backend api worker seed
	start_mockserver

	log "starting api on :$api_port"
	start_bg api "$REPO_ROOT/backend" "$BIN_DIR/api"
	wait_http "http://localhost:$api_port/healthz" api 90
	wait_http "http://localhost:$api_port/readyz" api 30

	log "running seeder"
	(cd "$REPO_ROOT/backend" && "$BIN_DIR/seed") 2>&1 | tee "$STATE_DIR/seed.log"

	start_bg worker "$REPO_ROOT/backend" "$BIN_DIR/worker"
	wait_worker 15 || die "worker failed to start"
	start_frontend "$fe_port"

	log "testing stack is up (config: $file)"
	echo "  mockserver http://localhost:$mock_port"
	echo "  api        http://localhost:$api_port"
	echo "  frontend   http://localhost:$fe_port"
	echo "  logs       $STATE_DIR/*.log"
}

cmd_down() {
	local dev_env="${1:-}" test_env="${2:-}" f ports="8080 5173"
	if [[ -n "$dev_env" ]]; then dev_env="$(abs_path "$dev_env")"; fi
	if [[ -n "$test_env" ]]; then test_env="$(abs_path "$test_env")"; fi
	for f in "$dev_env" "$test_env"; do
		if [[ -n "$f" && -f "$f" ]]; then ports="$ports $(stack_ports "$f")"; fi
	done
	# shellcheck disable=SC2046
	stop_stack $(printf '%s\n' $ports | sort -u)

	# Redis state of each env that exists. The testing wipe is skipped when it
	# shares a Redis DB with dev: that DB holds the dev queue.
	if [[ -n "$dev_env" && -f "$dev_env" ]]; then
		(load_env "$dev_env" && clean_redis dev)
	fi
	if [[ -n "$test_env" && -f "$test_env" ]]; then
		if [[ -n "$dev_env" && -f "$dev_env" && "$(redis_id "$dev_env")" == "$(redis_id "$test_env")" ]]; then
			warn "$test_env shares a Redis DB with $dev_env - leaving its queues alone"
		else
			(load_env "$test_env" && clean_redis testing)
		fi
	fi

	rm -rf "$BIN_DIR" "$STATE_DIR/air"
	log "down: processes stopped, ports free, runtime state cleaned (logs kept in $STATE_DIR)"
}

cmd_test_deps() {
	local file mock_port
	file="$(abs_path "$1")"
	[[ -f "$file" ]] || die "$file not found"
	load_env "$file"
	guard_test_db
	preflight_infra
	ensure_database "$DATABASE_URL"
	log "applying migrations to $(db_name "$DATABASE_URL")"
	(cd "$REPO_ROOT/backend" && go run ./cmd/migrate up)
	mock_port="${MOCK_PORT:-9090}"
	free_port "$mock_port"
	start_mockserver
}

cmd_init_test_env() {
	local file example jwt key
	file="$(abs_path "$1")"
	example="$(abs_path "$2")"
	[[ -f "$example" ]] || die "missing $example"
	jwt="$(openssl rand -base64 32)"
	key="$(openssl rand -base64 32)"
	sed -E -e "s|^JWT_SECRET=.*|JWT_SECRET=$jwt|" -e "s|^APP_ENCRYPTION_KEY=.*|APP_ENCRYPTION_KEY=$key|" "$example" >"$file"
	chmod 600 "$file"
	log "created $file from $(basename "$example") with freshly generated JWT_SECRET / APP_ENCRYPTION_KEY"
	log "keep it: settings stored in the test database are encrypted with that key"
}

case "${1:-}" in
up)
	case "${2:-}" in
	dev) cmd_up_dev "${3:-.env}" ;;
	testing) cmd_up_testing "${3:-.env.testing}" "${4:-}" ;;
	*) die "usage: stack.sh up dev|testing ENV_FILE" ;;
	esac
	;;
down) cmd_down "${2:-}" "${3:-}" ;;
test-deps) cmd_test_deps "${2:-.env.testing}" ;;
stop-mock)
	stop_pidfile mockserver
	rm -f "$BIN_DIR/mockserver"
	;;
init-test-env) cmd_init_test_env "${2:?FILE}" "${3:?EXAMPLE}" ;;
*) die "usage: stack.sh up dev|testing ENV_FILE | down [DEV_ENV [TEST_ENV]] | test-deps ENV_FILE | stop-mock | init-test-env FILE EXAMPLE" ;;
esac
