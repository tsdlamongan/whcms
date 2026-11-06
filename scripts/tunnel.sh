#!/usr/bin/env bash
# Starts a cloudflared quick tunnel to the local backend API (:8080) and
# prints the webhook endpoints this project exposes, so whichever public
# URL cloudflared prints can be pasted straight into a provider's sandbox
# dashboard (e.g. Duitku's callback URL) without hunting through the code.
set -euo pipefail

# shellcheck source=lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# The dev API's port: APP_PORT from .env (what `make up` started), else 8080.
PORT="${TUNNEL_PORT:-$(env_get "$REPO_ROOT/.env" APP_PORT)}"
PORT="${PORT:-8080}"

if ! command -v cloudflared >/dev/null 2>&1; then
	echo "cloudflared is not installed."
	echo "  macOS:  brew install cloudflared"
	echo "  other:  https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/"
	exit 1
fi

if ! curl -fsS -o /dev/null "http://localhost:${PORT}/healthz" 2>/dev/null; then
	echo "Backend API (:${PORT}) is not responding — run 'make up' first."
	exit 1
fi

cat <<EOF
Webhooks exposed by this project (inbound callbacks from external services):

  POST /api/v1/webhooks/duitku
      Duitku payment gateway callback — form-urlencoded, signature-verified, no auth.
      Registered dynamically as APP_BASE_URL + this path
      (see backend/internal/composition/build.go, docs/CONTRACTS.md §8 "Key flows").

Once cloudflared prints your https://<subdomain>.trycloudflare.com URL below,
set in .env (and run `make up` again) so Duitku's CallbackURL points at
the tunnel instead of localhost:

  APP_BASE_URL=https://<subdomain>.trycloudflare.com

==> Starting cloudflared tunnel -> http://localhost:${PORT}
EOF

exec cloudflared tunnel --url "http://localhost:${PORT}"
