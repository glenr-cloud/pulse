#!/bin/sh
# beacon export — lift this cell's signals out to a carried object.
#
# The task holds NO object-storage credential. It is handed a presigned PUT URL
# through the console's write-only variable surface at the moment it is needed,
# which is the whole point: the secret is a URL with an expiry, it never lands
# in the repository, and the product never stores a long-lived S3 key.
set -eu

fail() { printf 'beacon export: %s\n' "$1" >&2; exit 1; }

[ -n "${MAINDB_URL:-}" ] || fail "MAINDB_URL is not set — this task needs \`connects: [maindb]\` on its glenr.yaml row"
[ -n "${CARRY_PUT_URL:-}" ] || fail "CARRY_PUT_URL is not set — set it as a variable on this task (a presigned PUT URL for the carry object), then run again"

dump="$(mktemp)"
trap 'rm -f "$dump"' EXIT INT TERM

count="$(psql "$MAINDB_URL" -tAqc 'SELECT count(*) FROM public.signals')" ||
  fail "could not read public.signals — has the api created the table yet?"

# ☠️ DATA ONLY, ONE TABLE. A full dump would carry CREATE TABLE, and the
# destination's api has already created it on boot — the restore would fail on
# an object that already exists, mid-demo, with a message about a duplicate
# relation rather than about anything the operator did.
pg_dump "$MAINDB_URL" \
  --data-only --no-owner --no-privileges --table=public.signals >"$dump" ||
  fail "pg_dump failed — the export was NOT uploaded"

bytes="$(wc -c <"$dump" | tr -d ' ')"
[ "$bytes" -gt 0 ] || fail "pg_dump produced an empty file — refusing to upload it"

# ☠️ UPLOAD FROM A FILE, NEVER A PIPE. `-T -` makes curl send
# `Transfer-Encoding: chunked`, and S3-compatible endpoints reject a chunked PUT
# (501) because a presigned URL signs a Content-Length. Dumping to disk first
# also means a failed dump never becomes a truncated object.
curl -fsS --retry 3 --retry-connrefused --max-time 120 \
  -T "$dump" "$CARRY_PUT_URL" >/dev/null ||
  fail "the upload was refused — check the presigned PUT URL has not expired"

printf 'exported %s signals (%s bytes)\n' "$count" "$bytes"
