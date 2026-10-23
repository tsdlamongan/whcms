# Shared helpers for scripts/stack.sh and scripts/with-env.sh. Sourced, never
# executed on its own.
#
# Written for bash 3.2 (macOS /bin/bash): no associative arrays, mapfile or
# ${var,,}.

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE_DIR="$REPO_ROOT/.stack"
BIN_DIR="$STATE_DIR/bin"

log() { echo "[stack] $*"; }
warn() { echo "[stack] WARN: $*" >&2; }
die() {
	echo "[stack] ERROR: $*" >&2
	exit 1
}

# ---------------------------------------------------------------------------
# Env files
# ---------------------------------------------------------------------------

# _env_value turns the raw text after "KEY=" into the value, using the same
# rules as backend/internal/platform/config/envfile.go: a quoted value is taken
# literally (a '#' inside it stays); an unquoted one loses a trailing
# " # comment" ('#' preceded by space/tab); surrounding whitespace and one pair
# of quotes are removed.
_env_value() {
	local raw="$1" t i quoted=0
	t="${raw#"${raw%%[![:space:]]*}"}"
	t="${t%"${t##*[![:space:]]}"}"
	if ((${#t} >= 2)) && [[ ( "${t:0:1}" == '"' && "${t: -1}" == '"' ) || ( "${t:0:1}" == "'" && "${t: -1}" == "'" ) ]]; then
		quoted=1
	fi
	if ((!quoted)); then
		for ((i = 1; i < ${#raw}; i++)); do
			if [[ "${raw:i:1}" == "#" && ( "${raw:i-1:1}" == " " || "${raw:i-1:1}" == $'\t' ) ]]; then
				raw="${raw:0:i}"
				break
			fi
		done
		t="${raw#"${raw%%[![:space:]]*}"}"
		t="${t%"${t##*[![:space:]]}"}"
	fi
	if ((${#t} >= 2)) && [[ ( "${t:0:1}" == '"' && "${t: -1}" == '"' ) || ( "${t:0:1}" == "'" && "${t: -1}" == "'" ) ]]; then
		t="${t:1:${#t}-2}"
	fi
	printf '%s' "$t"
}

# load_env exports every non-empty KEY=VALUE of FILE. The file is parsed, never
# sourced (a DSN with '&' or a password with '$'/'#' would break `source`, and
# `include` in a Makefile). The file wins over variables already in the caller's
# environment, so a stray DATABASE_URL in your shell can't redirect a test run.
# Empty values are skipped: an exported-but-empty var would otherwise shadow the
# file the installation wizard rewrites (config.ApplyEnvFile never overrides a
# variable that already exists).
load_env() {
	local file="$1" line key val
	[[ -f "$file" ]] || die "env file not found: $file"
	while IFS= read -r line || [[ -n "$line" ]]; do
		line="${line#"${line%%[![:space:]]*}"}"
		[[ -z "$line" || "$line" == \#* ]] && continue
		line="${line#export }"
		[[ "$line" == *=* ]] || continue
		key="${line%%=*}"
		key="${key//[[:space:]]/}"
		[[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue
		val="$(_env_value "${line#*=}")"
		[[ -n "$val" ]] || continue
		export "$key=$val"
	done <"$file"
}

# env_get prints one value of FILE (empty if unset) without touching the
# caller's environment.
env_get() {
	local file="$1" key="$2"
	[[ -f "$file" ]] || return 0
	(
		load_env "$file"
		printf '%s' "${!key:-}"
	)
}

# abs_path makes a path absolute against the repo root (the Makefile passes
# paths relative to it).
abs_path() {
	case "$1" in
	/*) printf '%s' "$1" ;;
	*) printf '%s' "$REPO_ROOT/$1" ;;
	esac
}

# url_host_port prints "host port" of a URL or host:port string, ignoring
# scheme, user:password@ and path. With no explicit port it uses the scheme's
# (http 80, https 443), else $2.
url_host_port() {
	local rest host port
	rest="${1#*://}"
	rest="${rest##*@}"
	rest="${rest%%[/?]*}"
	host="${rest%%:*}"
	port="${rest#*:}"
	if [[ "$rest" != *:* || ! "$port" =~ ^[0-9]+$ ]]; then
		case "$1" in
		https://*) port=443 ;;
		http://*) port=80 ;;
		*) port="$2" ;;
		esac
	fi
	printf '%s %s' "${host:-localhost}" "$port"
}

url_port() {
	local host port
	read -r host port <<<"$(url_host_port "$1" "$2")"
	printf '%s' "$port"
}

# ---------------------------------------------------------------------------
# Processes and ports
# ---------------------------------------------------------------------------

# pid_alive uses `ps`, not `kill -0`: the latter also succeeds on a zombie that
# hasn't been reaped yet.
pid_alive() {
	local s
	s="$(ps -o stat= -p "$1" 2>/dev/null)" || return 1
	[[ -n "$s" && "$s" != Z* ]]
}

tree_pids() {
	local c
	echo "$1"
	for c in $(pgrep -P "$1" 2>/dev/null); do tree_pids "$c"; done
}

# stop_tree terminates a process and everything it spawned (`npm run dev` ->
# sh -> node, air -> its built binary, ...): TERM first, KILL after ~5s.
stop_tree() {
	local pids p i alive
	pids="$(tree_pids "$1")"
	for p in $pids; do kill -TERM "$p" 2>/dev/null || true; done
	for ((i = 0; i < 10; i++)); do
		alive=0
		for p in $pids; do pid_alive "$p" && alive=1; done
		((alive)) || return 0
		sleep 0.5
	done
	for p in $pids; do kill -KILL "$p" 2>/dev/null || true; done
}

proc_cwd() {
	lsof -a -p "$1" -d cwd -Fn 2>/dev/null | sed -n 's/^n//p' | head -n1
}

# A pidfile holds "<pid>\n<start time>". The start time guards against pid reuse:
# after a reboot the recorded pid can belong to an unrelated process.
write_pidfile() {
	mkdir -p "$STATE_DIR"
	printf '%s\n%s\n' "$2" "$(ps -o lstart= -p "$2" 2>/dev/null)" >"$STATE_DIR/$1.pid"
}

stop_pidfile() {
	local name="$1" file="$STATE_DIR/$1.pid" pid started
	[[ -f "$file" ]] || return 0
	pid="$(sed -n 1p "$file")"
	started="$(sed -n 2p "$file")"
	if [[ -n "$pid" ]] && pid_alive "$pid" && [[ "$(ps -o lstart= -p "$pid" 2>/dev/null)" == "$started" ]]; then
		log "stopping $name (pid $pid)"
		stop_tree "$pid"
	fi
	rm -f "$file"
}

port_pids() {
	lsof -nP -iTCP:"$1" -sTCP:LISTEN -t 2>/dev/null | sort -u
}

# free_port kills whatever is listening on PORT. It says what it killed, since
# the listener may be a stack you started by hand.
free_port() {
	local port="$1" pid i
	for pid in $(port_pids "$port"); do
		log "port $port is held by pid $pid ($(ps -o command= -p "$pid" 2>/dev/null | cut -c1-80)) - killing it"
		stop_tree "$pid"
	done
	for ((i = 0; i < 10; i++)); do
		[[ -z "$(port_pids "$port")" ]] && return 0
		sleep 0.3
	done
	die "port $port is still in use after killing its listener"
}

# sweep_strays kills this repo's leftovers that no pidfile (or port) accounts
# for: binaries built into .stack/bin, plus hand-started air / `go run` workers
# and their compiled children. Hand-started ones are matched by working
# directory (backend/), so another project's `cmd/worker` is never touched; an
# orphaned worker keeps consuming the shared asynq queue and runs stale code.
sweep_strays() {
	local pid cwd
	for pid in $(pgrep -f "$BIN_DIR/" 2>/dev/null); do
		pid_alive "$pid" || continue
		log "killing stray stack binary (pid $pid)"
		stop_tree "$pid"
	done
	for pid in $(pgrep -f '(^|/)air( |$)|go run .*cmd/(api|worker)|go-build[^ ]*/(api|worker)$|(^|/)tmp/(main|worker)( |$)' 2>/dev/null); do
		pid_alive "$pid" || continue
		cwd="$(proc_cwd "$pid")"
		[[ "$cwd" == "$REPO_ROOT/backend" ]] || continue
		log "killing stray backend process (pid $pid: $(ps -o command= -p "$pid" 2>/dev/null | cut -c1-80))"
		stop_tree "$pid"
	done
}

# wait_http polls URL until it answers. With ANY=1 any HTTP status counts (the
# installation wizard has no /healthz of the full API); otherwise it must be 2xx.
wait_http() {
	local url="$1" name="$2" tries="${3:-60}" any="${4:-0}" i flags=(-sS -o /dev/null)
	((any)) || flags+=(-f)
	for ((i = 0; i < tries; i++)); do
		if curl "${flags[@]}" "$url" 2>/dev/null; then
			log "$name is up ($url)"
			return 0
		fi
		sleep 1
	done
	warn "$name did not answer at $url within ${tries}s - last lines of $STATE_DIR/$name.log:"
	tail -n 40 "$STATE_DIR/$name.log" >&2 || true
	return 1
}

# ---------------------------------------------------------------------------
# Infrastructure (Postgres, Redis, S3) - taken from the loaded env
# ---------------------------------------------------------------------------

tcp_up() {
	(exec 3<>"/dev/tcp/$1/$2") 2>/dev/null
}

_check_tcp() {
	local host port
	read -r host port <<<"$(url_host_port "$2" "$3")"
	tcp_up "$host" "$port" && return 0
	warn "$1 is not reachable at $host:$port"
	return 1
}

# preflight_infra checks that Postgres, Redis and S3 answer where the loaded env
# says they are (so a remote dev database works too).
preflight_infra() {
	local ok=1
	_check_tcp PostgreSQL "${DATABASE_URL:-postgres://localhost:5432}" 5432 || ok=0
	_check_tcp Redis "${REDIS_ADDR:-localhost:6379}" 6379 || ok=0
	_check_tcp "RustFS/S3" "${RUSTFS_ENDPOINT:-http://localhost:9000}" 9000 || ok=0
	((ok)) || die "start the prerequisite services first (PostgreSQL 18, Redis, RustFS/MinIO) - see README.md"
}

# db_name / db_maintenance_url split a postgres:// URL into the database name and
# the same URL pointed at the "postgres" maintenance database.
db_name() {
	printf '%s' "$1" | sed -E 's#^[^/]*//[^/]*/([^?]*).*#\1#'
}
db_maintenance_url() {
	printf '%s' "$1" | sed -E 's#^([^/]*//[^/]*/)[^?]*#\1postgres#'
}

# ensure_database creates the database named in URL if it doesn't exist yet.
# Idempotent; never drops or recreates anything.
ensure_database() {
	local url="$1" name maint
	name="$(db_name "$url")"
	maint="$(db_maintenance_url "$url")"
	[[ "$name" =~ ^[A-Za-z0-9_]+$ ]] || die "refusing to create database with odd name '$name'"
	if ! psql "$maint" -tAc "SELECT 1 FROM pg_database WHERE datname = '$name'" 2>/dev/null | grep -q 1; then
		log "creating database '$name'"
		psql "$maint" -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$name\"" >/dev/null || die "could not create database '$name'"
	fi
}

# guard_test_db refuses to continue unless DATABASE_URL looks like a dedicated
# test database. Integration tests and the e2e seeder create and delete real
# rows; pointed at your dev database they would pollute it.
guard_test_db() {
	case "${DATABASE_URL:-}" in
	*_e2e* | *_test*) ;;
	*) die "DATABASE_URL does not look like a dedicated test database (expected a name containing _e2e or _test): ${DATABASE_URL:-<unset>}" ;;
	esac
}

# redis_del_pattern deletes the keys matching PATTERN in the Redis DB of the
# loaded env. Never FLUSHDB: that Redis is shared with other projects.
redis_del_pattern() {
	local pattern="$1" host port
	if ! command -v redis-cli >/dev/null 2>&1; then
		warn "redis-cli not found - skipping Redis cleanup of '$pattern'"
		return 0
	fi
	read -r host port <<<"$(url_host_port "${REDIS_ADDR:-localhost:6379}" 6379)"
	# REDISCLI_AUTH must stay unset without a password: an empty one makes
	# redis-cli send AUTH "" and fail every command.
	(
		if [[ -n "${REDIS_PASSWORD:-}" ]]; then export REDISCLI_AUTH="$REDIS_PASSWORD"; fi
		redis-cli -h "$host" -p "$port" -n "${REDIS_DB:-0}" --scan --pattern "$pattern" |
			tr '\n' '\0' |
			xargs -0 -n 200 redis-cli -h "$host" -p "$port" -n "${REDIS_DB:-0}" unlink >/dev/null
	) 2>/dev/null || true
}
