#!/usr/bin/env bash
# Postgres backup — gzipped pg_dump with a timestamped filename.
#
# Designed to run from cron / systemd.timer / k8s CronJob. The script picks
# up its DB credentials from PGHOST / PGPORT / PGUSER / PGPASSWORD / PGDATABASE
# (libpq's standard env vars) so the same script works in container & host.
#
# Usage:
#   PGHOST=postgres PGUSER=wuling PGDATABASE=wuling \
#   PGPASSWORD=... ./backup.sh [output_dir]
#
# Retention is OUT OF SCOPE here — pair this with `find $OUT -mtime +14 -delete`
# in your cron line, or hand things off to restic / borg / pgbackrest.

set -euo pipefail

OUT="${1:-/var/backups/wuling}"
mkdir -p "$OUT"

: "${PGHOST:=postgres}"
: "${PGPORT:=5432}"
: "${PGUSER:=wuling}"
: "${PGDATABASE:=wuling}"

stamp="$(date -u +%Y%m%d-%H%M%S)"
file="$OUT/wuling-${stamp}.sql.gz"

echo "[backup] -> $file"

# --no-owner / --no-privileges keep the dump portable across PG users.
pg_dump --no-owner --no-privileges --clean --if-exists \
        --host "$PGHOST" --port "$PGPORT" --username "$PGUSER" "$PGDATABASE" \
  | gzip -9 > "$file.tmp"

mv "$file.tmp" "$file"
echo "[backup] OK ($(stat -c%s "$file" 2>/dev/null || stat -f%z "$file") bytes)"
