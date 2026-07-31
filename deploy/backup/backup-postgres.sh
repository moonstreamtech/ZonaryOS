#!/usr/bin/env bash
# Copyright (c) ZonaryOS. All rights reserved.
# Use of this source code is governed by the license found in the LICENSE
# file in the root of this repository (draft, pending legal review - see
# docs/OPEN_POINTS.md item 20).
#
# Automated pg_dump backup for ZonaryOS's own Postgres on the Oracle Cloud
# dev/test instance (docs/OPEN_POINTS.md item 34, docs/DEPLOYMENT.md).
#
# PROVISIONAL DEFAULT, NOT A DECIDED SLA: docs/OPEN_POINTS.md item 25
# ("Backup Frequency / SLA Target") is explicitly still open - no concrete
# frequency/retention number has been approved by the Developer. This
# script hardcodes "once daily, keep the 7 most recent local dumps" only
# because *something* has to run before item 25 is finalized and because
# the task that produced this script asked for exactly that as a stated
# placeholder - not because 7 days is believed to be the right number.
# When item 25 is resolved, update RETENTION_COUNT below (and the cron/
# systemd cadence that invokes this script) to match the real decision,
# and delete this notice.
#
# Also out of scope here, by the same task's explicit instruction: no
# offsite/cloud copy (S3 or equivalent) of these dumps exists. Everything
# this script produces lives only on the single Oracle instance's local
# disk - a single-instance/single-region failure (or Oracle reclaiming
# the Always Free capacity, a documented real risk - see
# docs/OPEN_POINTS.md item 34) takes the backups with it. Flagged as a
# follow-up, not built here.
#
# CRITICAL SAFETY PROPERTY - do not weaken this: dumps are written to a
# plain host filesystem directory that Docker does not manage at all
# (default ~/zonaryos-backups, override via ZONARYOS_BACKUP_DIR), never
# to anything inside the `zonaryos-postgres-data` named volume
# (docker-compose.yml). This is deliberate, not incidental: a prior
# session on this project ran `docker compose down -v` before any real
# data existed, which deletes named volumes. If backups lived inside a
# Docker-managed volume (or anywhere `down -v` can reach), that exact
# mistake would destroy the live database AND every backup in the same
# command. Keeping backups on plain host disk, entirely outside Docker's
# management, is what makes that impossible.
#
# How the dump is taken: `docker compose exec` against the `postgres`
# service defined in docker-compose.yml (image postgres:16, listening on
# 5433 inside its own container namespace - see that file's own comments
# on why 5433, not 5432). Going through `docker compose exec` rather than
# a raw `docker exec <container-name>` deliberately avoids hardcoding the
# actual container name, which depends on the Compose project name (in
# turn derived from whatever directory the repo was cloned into - see
# docs/DEPLOYMENT.md step 2, `~/zonaryos-app` by convention but not
# enforced) - `docker compose -f ... exec postgres` resolves the right
# container by service name regardless of that project name.
set -euo pipefail

# --- Configuration (override via environment if needed) --------------------
REPO_DIR="${ZONARYOS_REPO_DIR:-$HOME/zonaryos-app}"
BACKUP_DIR="${ZONARYOS_BACKUP_DIR:-$HOME/zonaryos-backups}"
DB_USER="${ZONARYOS_DB_USER:-zonaryos}"
DB_NAME="${ZONARYOS_DB_NAME:-zonaryos}"
# PROVISIONAL default per docs/OPEN_POINTS.md item 25 - see notice above.
RETENTION_COUNT="${ZONARYOS_BACKUP_RETENTION_COUNT:-7}"

COMPOSE_FILES=(-f docker-compose.yml -f docker-compose.prod.yml)

log() { echo "[$(date -Is)] $*"; }
fail() { echo "[$(date -Is)] BACKUP FAILED: $*" >&2; exit 1; }

[ -d "$REPO_DIR" ] || fail "repo directory not found: $REPO_DIR (set ZONARYOS_REPO_DIR)"
command -v docker >/dev/null 2>&1 || fail "docker not found on PATH"

# Belt-and-braces: refuse to write backups anywhere under a path that looks
# like it could be inside Docker's own storage (e.g. someone overriding
# ZONARYOS_BACKUP_DIR to a mistaken location). This can't catch every
# misconfiguration, but it catches the specific, previously-seen mistake of
# pointing this at Docker-managed storage.
case "$BACKUP_DIR" in
  /var/lib/docker*) fail "refusing to back up into Docker-managed storage: $BACKUP_DIR" ;;
esac

mkdir -p "$BACKUP_DIR"

TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
DEST="$BACKUP_DIR/zonaryos-postgres-${TIMESTAMP}.sql.gz"
TMP_DEST="${DEST}.tmp"

log "starting dump of ${DB_NAME} (user ${DB_USER}) via 'docker compose exec postgres pg_dump' -> ${DEST}"

cd "$REPO_DIR"
if ! docker compose "${COMPOSE_FILES[@]}" exec -T postgres \
    pg_dump -U "$DB_USER" -d "$DB_NAME" | gzip > "$TMP_DEST"; then
  rm -f "$TMP_DEST"
  fail "pg_dump (or gzip) exited non-zero - no partial file left behind"
fi

# A dump that's suspiciously tiny (e.g. an auth failure that still exits 0,
# or an empty database that shouldn't be empty) is worth catching before
# rotation could discard a good older backup in its place.
SIZE_BYTES="$(stat -c%s "$TMP_DEST" 2>/dev/null || stat -f%z "$TMP_DEST")"
if [ "$SIZE_BYTES" -lt 100 ]; then
  rm -f "$TMP_DEST"
  fail "dump file suspiciously small (${SIZE_BYTES} bytes) - not keeping it, not rotating"
fi

mv "$TMP_DEST" "$DEST"
log "dump complete: ${DEST} (${SIZE_BYTES} bytes)"

# --- Rotation: keep the RETENTION_COUNT most recent dumps, delete older ---
# Provisional default (see notice above) - count-based, not calendar-based,
# so re-running this script more than once in a day (e.g. manual testing)
# doesn't prematurely age out a real daily backup.
mapfile -t ALL_DUMPS < <(ls -1t "$BACKUP_DIR"/zonaryos-postgres-*.sql.gz 2>/dev/null)
if [ "${#ALL_DUMPS[@]}" -gt "$RETENTION_COUNT" ]; then
  TO_DELETE=("${ALL_DUMPS[@]:$RETENTION_COUNT}")
  for f in "${TO_DELETE[@]}"; do
    log "rotating out old backup: $f"
    rm -f -- "$f"
  done
fi

log "backup script finished successfully (${#ALL_DUMPS[@]} dumps present before rotation, keeping newest ${RETENTION_COUNT})"
