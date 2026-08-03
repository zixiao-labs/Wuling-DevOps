#!/bin/sh
# Assert the built frontend bundle actually contains everything in public/.
#
# Nasti's build has no publicDir handling — the dev server serves public/ via
# sirv, but `nasti build` writes only what the module graph pulls in. We bridge
# that with the copy-public plugin in frontend/nasti.config.ts, and this script
# is the guard that the bridge is still standing: it is the difference between
# noticing a regression in CI and shipping a bundle with no favicon again.
#
# Also verifies that every root-relative asset index.html references was
# actually emitted, which catches a renamed file that the copy hid.
#
# Usage:  ./scripts/verify-brand-assets.sh [dist-dir]
#
# Set PUBLIC_DIR to point the source-of-truth elsewhere (the Docker build runs
# this from outside the repo layout, where the ../frontend guess does not hold).
#
# POSIX sh — must run on Alpine (no bash) used by Dockerfile.frontend.
set -eu

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
public_dir="${PUBLIC_DIR:-$repo_root/frontend/public}"
dist_dir="${1:-$repo_root/frontend/dist}"

[ -d "$public_dir" ] || {
  echo "verify-brand-assets: no source dir at $public_dir (override with PUBLIC_DIR)" >&2
  exit 1
}

[ -d "$dist_dir" ] || {
  echo "verify-brand-assets: no bundle at $dist_dir — run 'npm run build' first" >&2
  exit 1
}

status=0

# 1. Everything in public/ must have been copied into the bundle.
#    Here-doc (not process substitution) so status updates survive the loop
#    under plain /bin/sh.
while IFS= read -r rel; do
  [ -n "$rel" ] || continue
  if [ ! -f "$dist_dir/$rel" ]; then
    echo "MISSING from bundle: $rel (present in frontend/public/)" >&2
    status=1
  fi
done <<EOF
$(cd "$public_dir" && find . -type f ! -name '.DS_Store' | sed 's|^\./||')
EOF

# 2. Every root-relative file index.html points at must exist. Hashed /assets/*
#    are emitted by the bundler itself; this mainly covers the brand files,
#    which are referenced by stable name and so can rot silently.
if [ -f "$dist_dir/index.html" ]; then
  while IFS= read -r ref; do
    [ -n "$ref" ] || continue
    if [ ! -f "$dist_dir/$ref" ]; then
      echo "DANGLING reference in index.html: /$ref" >&2
      status=1
    fi
  done <<EOF
$(grep -oE '(href|src|content)="/[^"]+"' "$dist_dir/index.html" |
  sed -E 's/.*="\/([^"]+)"/\1/' | sort -u)
EOF
fi

# 3. The manifest's icons must resolve, or an install prompt silently degrades.
if [ -f "$dist_dir/manifest.json" ]; then
  while IFS= read -r icon; do
    [ -n "$icon" ] || continue
    if [ ! -f "$dist_dir/$icon" ]; then
      echo "DANGLING manifest icon: /$icon" >&2
      status=1
    fi
  done <<EOF
$(grep -oE '"src"[[:space:]]*:[[:space:]]*"/[^"]+"' "$dist_dir/manifest.json" |
  sed -E 's/.*"\/([^"]+)"/\1/' | sort -u)
EOF
fi

if [ "$status" -ne 0 ]; then
  echo >&2
  echo "Bundle is missing brand assets. If this fired after a Nasti upgrade," >&2
  echo "check that the copy-public plugin's closeBundle hook still runs —" >&2
  echo "see copyPublicDirPlugin in frontend/nasti.config.ts." >&2
  exit 1
fi

echo "verify-brand-assets: OK — public/ fully present in $(basename "$dist_dir"), no dangling references"
