#!/usr/bin/env bash
# fakegit.sh — drop-in stand-in for the real `git` binary used by
# internal/githttp tests. Records argv and stdin to env-named files, then
# prints a canned reply on stdout.
#
# Tests set Handler.GitBinary to the absolute path of this script and prime
# three environment variables before driving an HTTP request:
#
#   FAKEGIT_ARGS_FILE     — recipient file for the joined argv (one line)
#   FAKEGIT_STDIN_FILE    — recipient file for the request body bytes
#   FAKEGIT_STDOUT_PAYLOAD — bytes to write to stdout (may be empty)
#
# Optional:
#   FAKEGIT_EXIT_CODE     — exit code (default 0)
#
# We deliberately avoid `set -e`: a failing `printf` on a closed pipe (the
# httptest client may hang up early on some error paths) shouldn't surface as
# a non-zero exit and confuse the test.
set -u

if [[ -n "${FAKEGIT_ARGS_FILE:-}" ]]; then
  printf '%s\n' "$*" > "$FAKEGIT_ARGS_FILE"
fi

if [[ -n "${FAKEGIT_STDIN_FILE:-}" ]]; then
  cat > "$FAKEGIT_STDIN_FILE"
else
  cat > /dev/null
fi

if [[ -n "${FAKEGIT_STDOUT_PAYLOAD:-}" ]]; then
  printf '%s' "$FAKEGIT_STDOUT_PAYLOAD"
fi

exit "${FAKEGIT_EXIT_CODE:-0}"
