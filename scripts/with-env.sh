#!/usr/bin/env bash
# Run a command with an env file loaded into its environment.
#
#   scripts/with-env.sh .env.testing go test ./...
#
# The file is parsed with the same rules as the Go backend (see lib.sh), so
# values like a DSN with '&' or a password with '#' survive intact.
set -euo pipefail

# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

[[ $# -ge 2 ]] || die "usage: with-env.sh ENV_FILE command [args...]"
file="$(abs_path "$1")"
shift
load_env "$file"
exec "$@"
