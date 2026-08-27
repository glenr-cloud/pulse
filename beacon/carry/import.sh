#!/bin/sh
# beacon import — land a carried object into this cell's database.
#
# Safe to re-run: the restore replaces the table's contents rather than adding
# to them, so a rehearsal and the take produce the same rows.
set -eu

fail() { printf 'beacon import: %s\n' "$1" >&2; exit 1; }

[ -n "${MAINDB_URL:-}" ] || fail "MAINDB_URL is not set — this task needs \`connects: [maindb]\` on its glenr.yaml row"
[ -n "${CARRY_GET_URL:-}" ] || fail "CARRY_GET_URL is not set — set it as a variable on this task (a presigned GET URL for the carry object), then run again"

dump="$(mktemp)"
trap 'rm -f "$dump"' EXIT INT TERM

# ☠️ FETCH FIRST, TRUNCATE SECOND, AND NEVER THE OTHER WAY ROUND. A truncate
# that runs before a download that then fails has destroyed this cell's data to
# make room for nothing — on camera, irreversibly. Everything below this line
# is guarded by the download having already succeeded.
curl -fsS --retry 3 --retry-connrefused --max-time 120 \
  -o "$dump" "$CARRY_GET_URL" ||
  fail "the download was refused — check the presigned GET URL has not expired; nothing was changed"

[ -s "$dump" ] || fail "the carried object is empty — nothing was changed"
grep -q 'public\.signals' "$dump" ||
  fail "the carried object does not mention public.signals, so it is not a beacon export — nothing was changed"

# One transaction: the truncate, the restore and the sequence fix either all
# land or none do. A half-applied carry would leave the board empty.
#
# ☠️ THE SEQUENCE FIX IS NOT OPTIONAL. A data-only dump carries the `id` values
# but not the sequence's position, so without setval the destination's bigserial
# still points at 1 — and the FIRST signal anyone posts after the migration
# fails on a duplicate key. That is precisely the moment the demo hands the
# keyboard to the audience.
#
# ☠️ AND EVERY NAME HERE IS SCHEMA-QUALIFIED, because pg_dump's own preamble
# runs `set_config('search_path', '', false)` inside this same session — an
# unqualified `signals` after the restore file would not resolve.
psql "$MAINDB_URL" -v ON_ERROR_STOP=1 --single-transaction -q \
  -c 'TRUNCATE public.signals' \
  -f "$dump" \
  -c "SELECT setval(pg_get_serial_sequence('public.signals','id'), GREATEST(COALESCE((SELECT max(id) FROM public.signals), 1), 1))" \
  >/dev/null ||
  fail "the restore failed and was rolled back — this cell's signals are unchanged"

count="$(psql "$MAINDB_URL" -tAqc 'SELECT count(*) FROM public.signals')" ||
  fail "the restore succeeded but the count could not be read"

printf 'carried %s signals\n' "$count"
