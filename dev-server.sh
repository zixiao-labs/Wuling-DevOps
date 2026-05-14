#!/usr/bin/env bash
# Wuling DevOps — one-shot local dev launcher.
# Boots Postgres (Docker), runs migrations, then starts the Go API and the
# nasti frontend together with Ctrl+C cleanup. Postgres is intentionally left
# running on exit so the next launch is fast.

echo "Wellcome Wuling"

echo "██╗    ██╗██╗   ██╗██╗     ██╗███╗   ██╗ ██████╗     ██████╗ ███████╗██╗   ██╗ ██████╗ ██████╗ ███████╗"
echo "██║    ██║██║   ██║██║     ██║████╗  ██║██╔════╝     ██╔══██╗██╔════╝██║   ██║██╔═══██╗██╔══██╗██╔════╝"
echo "██║ █╗ ██║██║   ██║██║     ██║██╔██╗ ██║██║  ███╗    ██║  ██║█████╗  ██║   ██║██║   ██║██████╔╝███████╗"
echo "██║███╗██║██║   ██║██║     ██║██║╚██╗██║██║   ██║    ██║  ██║██╔══╝  ╚██╗ ██╔╝██║   ██║██╔═══╝ ╚════██║"
echo "╚███╔███╔╝╚██████╔╝███████╗██║██║ ╚████║╚██████╔╝    ██████╔╝███████╗ ╚████╔╝ ╚██████╔╝██║     ███████║"
echo " ╚══╝╚══╝  ╚═════╝ ╚══════╝╚═╝╚═╝  ╚═══╝ ╚═════╝     ╚═════╝ ╚══════╝  ╚═══╝   ╚═════╝ ╚═╝     ╚══════╝"

echo "Development Environment Requirements"
echo "Node.js: 24"
echo "Golang: 1.25"
echo "Docker (with compose v2)"
echo "nix (optional)"
echo "Please ensure that it is installed and that your office is not on Terra or Talos-II, otherwise some bad things may happen (such as undefined behavior, startup failure, unexplained bugs)."

echo "Are you sure you want to continue? [Y/n]"
read -r answer
# Empty input defaults to "yes" so the prompt matches the [Y/n] convention.
if [[ $answer == "n" || $answer == "N" ]]; then
    echo "Aborting."
    exit 1
fi

set -euo pipefail
cd "$(dirname "$0")"

require() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "missing dependency: $1 — install it and retry." >&2
        exit 1
    fi
}
require go
require node
require npm
require docker

# Docker on its own isn't enough — we use `docker compose` (v2) below, and
# v1's `docker-compose` standalone binary has a different invocation. Fail
# fast with a clear message if v2 isn't available.
compose_version=$(docker compose version 2>&1 || true)
if ! echo "$compose_version" | grep -qE "v2|version 2"; then
    echo "error: Docker Compose v2 not available (got: $compose_version)." >&2
    echo "       Install Docker Desktop with Compose v2 and retry." >&2
    exit 1
fi

export WULING_ENV=${WULING_ENV:-dev}
export WULING_HTTP_ADDR=${WULING_HTTP_ADDR:-:8080}
export WULING_DB_DSN=${WULING_DB_DSN:-postgres://wuling:wuling@localhost:5432/wuling?sslmode=disable}
export WULING_REPO_ROOT=${WULING_REPO_ROOT:-$(pwd)/var/repos}
export WULING_JWT_SECRET=${WULING_JWT_SECRET:-dev-only-not-a-real-secret}
export WULING_LOG_FORMAT=${WULING_LOG_FORMAT:-text}

if [ "$WULING_JWT_SECRET" = "dev-only-not-a-real-secret" ]; then
    echo "WARNING: using dev-only-not-a-real-secret default JWT secret — for local development only; override WULING_JWT_SECRET in staging/CI/prod" >&2
fi

mkdir -p "$WULING_REPO_ROOT"

echo "→ starting postgres (docker compose)…"
docker compose -f deploy/docker-compose.yml up -d postgres

echo "→ waiting for postgres to accept connections…"
pg_ready=0
for _ in {1..30}; do
    if docker compose -f deploy/docker-compose.yml exec -T postgres pg_isready -U wuling -d wuling >/dev/null 2>&1; then
        pg_ready=1
        break
    fi
    sleep 1
done
if [ "$pg_ready" -ne 1 ]; then
    echo "error: postgres did not become ready within 30s; aborting." >&2
    exit 1
fi

echo "→ applying database migrations…"
go run ./cmd/wuling-migrate up

if [ ! -d frontend/node_modules ]; then
    echo "→ installing frontend dependencies (first run)…"
    (cd frontend && npm install)
fi

pids=()
cleanup() {
    trap - EXIT INT TERM
    echo
    echo "→ shutting down dev services…"
    if [ "${#pids[@]}" -gt 0 ]; then
        for pid in "${pids[@]}"; do
            if kill -0 "$pid" 2>/dev/null; then
                kill "$pid" 2>/dev/null || true
            fi
        done
    fi
    wait 2>/dev/null || true
    echo "  (postgres is still running — \`docker compose -f deploy/docker-compose.yml down\` to stop it.)"
}
trap cleanup EXIT INT TERM

echo "→ starting wuling-api on $WULING_HTTP_ADDR…"
go run ./cmd/wuling-api &
pids+=($!)

echo "→ starting frontend on http://localhost:3000…"
(cd frontend && npm run dev) &
pids+=($!)

# Derive the displayed URL from WULING_HTTP_ADDR so it tracks the actual
# bind address (":8080", "0.0.0.0:8080", "127.0.0.1:9000", ...).
display_addr=${WULING_HTTP_ADDR:-:8080}
case "$display_addr" in
    :*) api_url="http://localhost${display_addr}" ;;
    http://*|https://*) api_url="$display_addr" ;;
    *) api_url="http://${display_addr}" ;;
esac

cat <<EOF

── Wuling DevOps dev environment ───────────────────────
  API:       ${api_url}
  Frontend:  http://localhost:3000
  Postgres:  localhost:5432 (wuling/wuling)
────────────────────────────────────────────────────────

Press Ctrl+C to stop.

EOF

# Block until either service exits; the EXIT trap reaps the survivor.
while kill -0 "${pids[0]}" 2>/dev/null && kill -0 "${pids[1]}" 2>/dev/null; do
    sleep 1
done
echo "→ a dev service exited; tearing down the rest."
