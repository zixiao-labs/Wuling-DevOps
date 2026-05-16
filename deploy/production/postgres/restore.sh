#!/usr/bin/env bash
# Postgres restore — pipes a gzipped pg_dump back through psql.
#
# THIS IS DESTRUCTIVE: it runs DROP/CREATE statements baked into the dump
# (we used --clean --if-exists at backup time). Make sure the API process is
# stopped first so connections don't fight with the rewrite.
#
# Usage:
#   ./restore.sh /var/backups/wuling/wuling-20260513-021500.sql.gz

set -euo pipefail

if [ "$#" -lt 1 ]; then
  echo "usage: $0 <wuling-YYYYMMDD-HHMMSS.sql.gz>" >&2
  exit 64
fi

src="$1"
[ -f "$src" ] || { echo "no such file: $src" >&2; exit 66; }

: "${PGHOST:=postgres}"
: "${PGPORT:=5432}"
: "${PGUSER:=wuling}"
: "${PGDATABASE:=wuling}"

echo "[restore] $src -> $PGUSER@$PGHOST:$PGPORT/$PGDATABASE"
echo "[restore] WARNING: this overwrites the current database. Ctrl-C in 5s to abort."
sleep 5

gunzip -c "$src" \
  | psql --host "$PGHOST" --port "$PGPORT" --username "$PGUSER" "$PGDATABASE" \
         --set ON_ERROR_STOP=on --quiet

echo "[restore] OK"
