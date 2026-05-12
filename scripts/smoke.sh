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
WORK=""
cleanup() {
  if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
    echo "stopping wuling-api ($server_pid)"
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  if [ -n "$WORK" ]; then
    rm -rf "$WORK"
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

# ---- merge requests -------------------------------------------------------
#
# Spin up a fresh repo, push two diverging branches, then walk the MR API
# end to end across all three merge strategies.

REPO_SLUG="repo${SUFFIX}"
echo "creating repo $REPO_SLUG"
api POST "/api/v1/orgs/$ORG_SLUG/projects/$PROJECT_SLUG/repos" \
  "$(printf '{"slug":"%s","default_branch":"main"}' "$REPO_SLUG")" >/dev/null

# Create a PAT so `git push` over HTTP basic auth works against the API.
echo "minting PAT"
pat_resp=$(api POST /api/v1/auth/tokens '{"name":"smoke","scopes":["repo:read","repo:write"]}')
PAT=$(printf '%s' "$pat_resp" | py '["token"]')

# Strip "http://" → "user:pat@host" auth-bearing URL.
proto="${BASE%%://*}"
host_port="${BASE#*://}"
REPO_URL="${proto}://${USER}:${PAT}@${host_port}/${ORG_SLUG}/${PROJECT_SLUG}/${REPO_SLUG}.git"

WORK=$(mktemp -d -t wuling-smoke-XXXXXX)
(
  cd "$WORK"
  git init -q -b main
  git config user.email "smoke@example.test"
  git config user.name  "smoke"
  echo "hello" > README.md
  git add README.md
  git commit -q -m "initial commit"
  git remote add origin "$REPO_URL"
  git push -q origin main
) || { echo "initial push failed" >&2; exit 1; }

mrs_path="/api/v1/orgs/$ORG_SLUG/projects/$PROJECT_SLUG/repos/$REPO_SLUG/merge-requests"

# --- FF MR ---

echo "creating ff branch"
(
  cd "$WORK"
  git checkout -q -b feat/ff
  echo "ff" >> README.md
  git commit -q -am "ff: append line"
  git push -q origin feat/ff
) || { echo "ff push failed" >&2; exit 1; }

echo "opening MR (ff)"
ff_resp=$(api POST "$mrs_path" \
  '{"title":"smoke ff","source_ref":"feat/ff","target_ref":"main"}')
ff_number=$(printf '%s' "$ff_resp" | py '["number"]')
echo "  MR #$ff_number"

echo "diff without patch"
diff_resp=$(api GET "$mrs_path/$ff_number/diff")
files=$(printf '%s' "$diff_resp" | py_len '["files"]')
if [ "$files" -ne 1 ]; then echo "expected 1 file in diff, got $files" >&2; exit 1; fi
add=$(printf '%s' "$diff_resp" | py '["files"][0]["additions"]')
patch=$(printf '%s' "$diff_resp" | py '["files"][0].get("patch","")')
if [ "$add" -ne 1 ]; then echo "expected 1 addition, got $add" >&2; exit 1; fi
if [ -n "$patch" ]; then echo "patch leaked without include=patch" >&2; exit 1; fi

echo "diff with patch"
patch_resp=$(api GET "$mrs_path/$ff_number/diff?include=patch")
patch=$(printf '%s' "$patch_resp" | py '["files"][0]["patch"]')
if [ -z "$patch" ]; then echo "expected patch text, got empty" >&2; exit 1; fi

echo "commits in MR"
cmts=$(api GET "$mrs_path/$ff_number/commits" | py_len '["commits"]')
if [ "$cmts" -ne 1 ]; then echo "expected 1 commit in MR, got $cmts" >&2; exit 1; fi

echo "merging MR (ff)"
merged=$(api POST "$mrs_path/$ff_number/merge" '{"strategy":"ff"}')
state=$(printf '%s' "$merged" | py '["state"]')
oid=$(printf '%s' "$merged" | py '["merge_commit_oid"]')
if [ "$state" != "merged" ]; then echo "expected state=merged, got $state" >&2; exit 1; fi
if [ -z "$oid" ] || [ "$oid" = "None" ]; then echo "expected merge_commit_oid, got '$oid'" >&2; exit 1; fi

# --- merge-commit MR ---

echo "creating merge-commit branch"
(
  cd "$WORK"
  git fetch -q origin
  # Make main diverge: commit on main, then a separate branch off the prior tip.
  git checkout -q main
  git pull -q --ff-only origin main
  echo "main-only" >> NOTES.md
  git add NOTES.md
  git commit -q -m "main: notes"
  git push -q origin main

  git checkout -q -b feat/mc HEAD~1
  echo "branch-only" >> BRANCH.md
  git add BRANCH.md
  git commit -q -m "mc: branch file"
  git push -q origin feat/mc
) || { echo "mc push failed" >&2; exit 1; }

echo "opening MR (merge-commit)"
mc_resp=$(api POST "$mrs_path" \
  '{"title":"smoke mc","source_ref":"feat/mc","target_ref":"main"}')
mc_number=$(printf '%s' "$mc_resp" | py '["number"]')

echo "merging MR (merge-commit)"
mc_merged=$(api POST "$mrs_path/$mc_number/merge" '{"strategy":"merge-commit"}')
mc_state=$(printf '%s' "$mc_merged" | py '["state"]')
if [ "$mc_state" != "merged" ]; then echo "mc: expected merged, got $mc_state" >&2; exit 1; fi

# Verify the new commit on main has 2 parents (true merge commit).
parents=$(api GET "/api/v1/orgs/$ORG_SLUG/projects/$PROJECT_SLUG/repos/$REPO_SLUG/commits?ref=main&limit=1" \
  | py_len '["commits"][0]["parents"]')
if [ "$parents" -ne 2 ]; then echo "mc: expected 2 parents on tip, got $parents" >&2; exit 1; fi

# --- squash MR ---

echo "creating squash branch"
(
  cd "$WORK"
  git fetch -q origin
  git checkout -q main
  git pull -q --ff-only origin main
  echo "main-2" >> NOTES.md
  git commit -q -am "main: more notes"
  git push -q origin main

  git checkout -q -b feat/sq HEAD~1
  echo "sq-1" > SQ.md
  git add SQ.md
  git commit -q -m "sq: file 1"
  echo "sq-2" >> SQ.md
  git commit -q -am "sq: file 2"
  git push -q origin feat/sq
) || { echo "sq push failed" >&2; exit 1; }

echo "opening MR (squash)"
sq_resp=$(api POST "$mrs_path" \
  '{"title":"smoke sq","source_ref":"feat/sq","target_ref":"main"}')
sq_number=$(printf '%s' "$sq_resp" | py '["number"]')

echo "merging MR (squash)"
sq_merged=$(api POST "$mrs_path/$sq_number/merge" '{"strategy":"squash"}')
sq_state=$(printf '%s' "$sq_merged" | py '["state"]')
if [ "$sq_state" != "merged" ]; then echo "sq: expected merged, got $sq_state" >&2; exit 1; fi

# Squash should produce a single-parent commit on main.
sq_parents=$(api GET "/api/v1/orgs/$ORG_SLUG/projects/$PROJECT_SLUG/repos/$REPO_SLUG/commits?ref=main&limit=1" \
  | py_len '["commits"][0]["parents"]')
if [ "$sq_parents" -ne 1 ]; then echo "sq: expected 1 parent on tip, got $sq_parents" >&2; exit 1; fi

# --- comments + reviews ---

echo "MR comment + review"
api POST "$mrs_path/$ff_number/comments" '{"body":"+1 from smoke"}' >/dev/null
review_state=$(api POST "$mrs_path/$ff_number/reviews" '{"state":"approved","body":"lgtm"}' | py '["state"]')
if [ "$review_state" != "approved" ]; then echo "review: expected approved, got $review_state" >&2; exit 1; fi
review_count=$(api GET "$mrs_path/$ff_number/reviews" | py_len '["reviews"]')
if [ "$review_count" -ne 1 ]; then echo "expected 1 review, got $review_count" >&2; exit 1; fi

# --- merged-state list filter ---

merged_count=$(api GET "$mrs_path?state=merged" | py_len '["merge_requests"]')
if [ "$merged_count" -lt 3 ]; then echo "expected >=3 merged MRs, got $merged_count" >&2; exit 1; fi

# ---- wiki ----------------------------------------------------------------

wiki_path="/api/v1/orgs/$ORG_SLUG/projects/$PROJECT_SLUG/wiki"

echo "creating wiki Home.md"
home_resp=$(api PUT "$wiki_path/pages/Home.md" \
  '{"content":"# Home\n\nhello from smoke.sh"}')
home_oid=$(printf '%s' "$home_resp" | py '["commit_oid"]')
if [ -z "$home_oid" ]; then echo "wiki: expected commit_oid, got empty" >&2; exit 1; fi
home_html=$(printf '%s' "$home_resp" | py '["html"]')
case "$home_html" in
  *"<h1>"*) : ;;
  *) echo "wiki: rendered HTML missing <h1>: $home_html" >&2; exit 1 ;;
esac

echo "creating nested wiki page docs/usage.md"
api PUT "$wiki_path/pages/docs/usage.md" '{"content":"## Usage"}' >/dev/null

echo "listing wiki pages"
page_count=$(api GET "$wiki_path/pages" | py_len '["pages"]')
if [ "$page_count" -lt 2 ]; then echo "wiki: expected >=2 pages, got $page_count" >&2; exit 1; fi

echo "fetching wiki Home.md"
got_html=$(api GET "$wiki_path/pages/Home.md" | py '["html"]')
case "$got_html" in
  *"<h1"*) : ;;
  *) echo "wiki: GET Home.md HTML missing <h1>: $got_html" >&2; exit 1 ;;
esac

echo "wiki history"
hist=$(api GET "$wiki_path/history" | py_len '["commits"]')
if [ "$hist" -lt 2 ]; then echo "wiki: expected >=2 commits, got $hist" >&2; exit 1; fi

echo "deleting wiki Home.md"
del_status=$(curl -fsS -o /dev/null -w "%{http_code}" \
  -X DELETE -H "Authorization: Bearer $TOKEN" "$BASE$wiki_path/pages/Home.md") || true
if [ "$del_status" != "204" ]; then echo "wiki: delete expected 204, got $del_status" >&2; exit 1; fi

# ---- insights ------------------------------------------------------------
#
# The receive-pack hook indexes commits asynchronously. Poll briefly so the
# rollup is populated before we assert against it.

insights_path="/api/v1/orgs/$ORG_SLUG/projects/$PROJECT_SLUG/insights"

echo "polling insights activity"
ok=0
for _ in {1..30}; do
  total=$(api GET "$insights_path/activity?since=7d" \
    | python3 -c 'import json,sys; d=json.load(sys.stdin); print(sum(x["commits"] for x in d["days"]))')
  if [ "$total" -gt 0 ]; then ok=1; break; fi
  sleep 0.5
done
if [ "$ok" -ne 1 ]; then echo "insights: never saw a commit in activity rollup" >&2; exit 1; fi

echo "insights contributors"
contrib_count=$(api GET "$insights_path/contributors?repo=$REPO_SLUG&since=30d" | py_len '["contributors"]')
if [ "$contrib_count" -lt 1 ]; then echo "insights: expected >=1 contributor, got $contrib_count" >&2; exit 1; fi

echo "insights languages"
md_bytes=$(api GET "$insights_path/languages?repo=$REPO_SLUG" \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["bytes"].get("Markdown",0))')
if [ "$md_bytes" -lt 1 ]; then echo "insights: expected Markdown bytes >=1, got $md_bytes" >&2; exit 1; fi

# ---- SSH transport -------------------------------------------------------
#
# Skip if openssh client tools aren't installed or the sshd port is closed.

if command -v ssh-keygen >/dev/null 2>&1 && command -v ssh >/dev/null 2>&1; then
  SSH_HOST=${WULING_SSH_HOST:-127.0.0.1}
  SSH_PORT=${WULING_SSH_PORT:-2222}
  if (echo >"/dev/tcp/${SSH_HOST}/${SSH_PORT}") >/dev/null 2>&1; then
    echo "registering SSH key + pushing over SSH"
    SSH_KEY="$WORK/id_ed25519"
    ssh-keygen -q -t ed25519 -N "" -f "$SSH_KEY" -C "smoke@example.test"
    pub=$(cat "${SSH_KEY}.pub")
    api POST /api/v1/auth/ssh-keys "$(python3 -c "import json,sys; print(json.dumps({'title':'smoke','public_key':sys.argv[1]}))" "$pub")" >/dev/null

    SSH_REPO="ssh://wuling@${SSH_HOST}:${SSH_PORT}/${ORG_SLUG}/${PROJECT_SLUG}/${REPO_SLUG}.git"
    GIT_SSH_COMMAND="ssh -i $SSH_KEY -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o IdentitiesOnly=yes"
    SSH_CLONE="$WORK/ssh-clone"
    (
      cd "$WORK"
      GIT_SSH_COMMAND="$GIT_SSH_COMMAND" git clone -q "$SSH_REPO" "$SSH_CLONE"
      cd "$SSH_CLONE"
      git config user.email "smoke@example.test"
      git config user.name "smoke"
      echo "from-ssh" >> README.md
      git commit -q -am "ssh: append line"
      GIT_SSH_COMMAND="$GIT_SSH_COMMAND" git push -q origin main
    ) || { echo "ssh git flow failed" >&2; exit 1; }
  else
    echo "ssh port ${SSH_HOST}:${SSH_PORT} not listening — skipping SSH transport smoke"
  fi
else
  echo "ssh / ssh-keygen not on PATH — skipping SSH transport smoke"
fi

echo "smoke test passed."
