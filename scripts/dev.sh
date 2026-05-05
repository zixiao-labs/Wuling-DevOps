#!/usr/bin/env bash
# scripts/dev.sh — boot a local Postgres in Docker and run the API natively.
# Native run keeps cgo iteration fast (no image rebuild on every edit).
set -euo pipefail

cd "$(dirname "$0")/.."

docker compose -f deploy/docker-compose.yml up -d postgres

export WULING_ENV=${WULING_ENV:-dev}
export WULING_HTTP_ADDR=${WULING_HTTP_ADDR:-:8080}
export WULING_DB_DSN=${WULING_DB_DSN:-postgres://wuling:wuling@localhost:5432/wuling?sslmode=disable}
export WULING_REPO_ROOT=${WULING_REPO_ROOT:-$(pwd)/var/repos}
export WULING_JWT_SECRET=${WULING_JWT_SECRET:-dev-only-not-a-real-secret}
export WULING_LOG_FORMAT=${WULING_LOG_FORMAT:-text}

mkdir -p "$WULING_REPO_ROOT"

# Wait for postgres to accept connections.
pg_ready=0
for i in {1..30}; do
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

go run ./cmd/wuling-migrate up
exec go run ./cmd/wuling-api
