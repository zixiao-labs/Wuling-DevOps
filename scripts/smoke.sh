#!/usr/bin/env bash
# scripts/smoke.sh — end-to-end smoke test for the API.
#
# Boots the API via scripts/dev.sh in the background, then exercises the
# Stage-1 surface end to end:
#   - register / login
#   - create org → project
#   - create issue → list → comment → close
#
# The script kills the API on exit, so it's safe to re-run. Override
# WULING_HTTP_BASE if you're hitting a server that's already running and
# set WULING_SMOKE_START_SERVER=0 to skip the boot step.
set -euo pipefail

cd "$(dirname "$0")/.."

BASE=${WULING_HTTP_BASE:-http://localhost:8080}
START_SERVER=${WULING_SMOKE_START_SERVER:-1}

# Pick a fresh username/org/project per run so this works against a database
# that's already seen earlier runs. RANDOM is plenty for dev-box uniqueness.
SUFFIX=${WULING_SMOKE_SUFFIX:-$RANDOM$RANDOM}
USER="smoke${SUFFIX}"
EMAIL="${USER}@example.test"
PASSWORD="smoke-password-1"
ORG_SLUG="org${SUFFIX}"
PROJECT_SLUG="proj${SUFFIX}"

server_pid=""
cleanup() {
  if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
    echo "stopping wuling-api ($server_pid)"
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT

if [ "$START_SERVER" = "1" ]; then
  ./scripts/dev.sh >/tmp/wuling-smoke.log 2>&1 &
  server_pid=$!

  echo "waiting for $BASE/healthz"
  ready=0
  for _ in {1..60}; do
    if curl -fsS "$BASE/healthz" >/dev/null 2>&1; then
      ready=1; break
    fi
    sleep 0.5
  done
  if [ "$ready" -ne 1 ]; then
    echo "API never came up; tail of log:" >&2
    tail -50 /tmp/wuling-smoke.log >&2 || true
    exit 1
  fi
fi

# ---- helpers --------------------------------------------------------------
#
# We use python3 for JSON parsing rather than jq so the script doesn't add a
# new dependency. py extracts a single dotted path; py_len returns the length
# of an array at a given path.

py() {
  # py FIELD < json -> stdout
  python3 -c "import json,sys; print(json.load(sys.stdin)$1)"
}
py_len() {
  python3 -c "import json,sys; print(len(json.load(sys.stdin)$1))"
}

api() {
  # api METHOD PATH [JSON]
  local method=$1 path=$2 body=${3:-}
  local args=(-fsS -X "$method" "$BASE$path"
              -H 'Content-Type: application/json'
              -H "Authorization: Bearer ${TOKEN:-}")
  if [ -n "$body" ]; then args+=(-d "$body"); fi
  curl "${args[@]}"
}

# ---- register + login -----------------------------------------------------

echo "registering $USER"
register_resp=$(curl -fsS -X POST "$BASE/api/v1/auth/register" \
  -H 'Content-Type: application/json' \
  -d "$(printf '{"username":"%s","email":"%s","password":"%s"}' "$USER" "$EMAIL" "$PASSWORD")")
TOKEN=$(printf '%s' "$register_resp" | py '["access_token"]')
echo "  got token (${#TOKEN} chars)"

# ---- create org + project -------------------------------------------------

echo "creating org $ORG_SLUG"
api POST /api/v1/orgs "$(printf '{"slug":"%s"}' "$ORG_SLUG")" >/dev/null

echo "creating project $PROJECT_SLUG"
api POST "/api/v1/orgs/$ORG_SLUG/projects" "$(printf '{"slug":"%s"}' "$PROJECT_SLUG")" >/dev/null

# ---- issues ---------------------------------------------------------------

issues_path="/api/v1/orgs/$ORG_SLUG/projects/$PROJECT_SLUG/issues"

echo "creating issue"
issue_resp=$(api POST "$issues_path" '{"title":"smoke: hello issues","body":"first issue from smoke.sh"}')
issue_number=$(printf '%s' "$issue_resp" | py '["number"]')
echo "  got issue #$issue_number"

echo "creating second issue (will stay open)"
api POST "$issues_path" '{"title":"smoke: second"}' >/dev/null

echo "listing issues (open)"
open_count=$(api GET "$issues_path?state=open" | py_len '["issues"]')
echo "  $open_count open issue(s)"
if [ "$open_count" -lt 2 ]; then
  echo "expected at least 2 open issues, got $open_count" >&2
  exit 1
fi

echo "fetching issue #$issue_number"
api GET "$issues_path/$issue_number" >/dev/null

# ---- comments -------------------------------------------------------------

echo "posting comment"
api POST "$issues_path/$issue_number/comments" '{"body":"+1 from smoke.sh"}' >/dev/null

echo "listing comments"
comments_count=$(api GET "$issues_path/$issue_number/comments" | py_len '["comments"]')
echo "  $comments_count comment(s)"
if [ "$comments_count" -ne 1 ]; then
  echo "expected 1 comment, got $comments_count" >&2
  exit 1
fi

# ---- close ---------------------------------------------------------------

echo "closing issue #$issue_number"
state=$(api PATCH "$issues_path/$issue_number" '{"state":"closed"}' | py '["state"]')
if [ "$state" != "closed" ]; then
  echo "expected state=closed, got $state" >&2
  exit 1
fi

closed_count=$(api GET "$issues_path?state=closed" | py_len '["issues"]')
echo "  $closed_count closed issue(s)"
if [ "$closed_count" -lt 1 ]; then
  echo "expected at least 1 closed issue" >&2
  exit 1
fi

echo "smoke test passed."
