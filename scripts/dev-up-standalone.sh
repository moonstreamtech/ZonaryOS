#!/usr/bin/env bash
# Fallback for `make dev-up` in environments where Docker registries
# (Docker Hub, quay.io) are blocked but general internet/GitHub access
# isn't - some sandboxed CI/agent environments allow the latter but not
# the former. Starts the same two services (Postgres, Keycloak) natively
# instead of via docker-compose:
#   - Postgres: expected to already be installed locally (apt/brew/etc,
#     same as this repo's RLS integration tests already assume - see
#     docs/DEVELOPMENT.md); this script only starts it and creates the
#     zonaryos database/role if missing.
#   - Keycloak: downloaded as the standalone Quarkus distribution from
#     GitHub Releases (a different network path than Docker registries)
#     into .zonaryos/keycloak-standalone/, then started with
#     `--import-realm` against the same deploy/keycloak/zonaryos-realm.json
#     used by the Docker path.
#
# Not a general Docker replacement - it exists solely so this repo's
# Keycloak integration can be verified in environments where `make dev-up`
# cannot pull images. Prefer `make dev-up` whenever Docker registries are
# reachable.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KEYCLOAK_VERSION="26.0.7"
KEYCLOAK_DIR="$REPO_ROOT/.zonaryos/keycloak-standalone"
KEYCLOAK_HOME="$KEYCLOAK_DIR/keycloak-$KEYCLOAK_VERSION"
KEYCLOAK_ZIP="$KEYCLOAK_DIR/keycloak-$KEYCLOAK_VERSION.zip"
KEYCLOAK_URL="https://github.com/keycloak/keycloak/releases/download/$KEYCLOAK_VERSION/keycloak-$KEYCLOAK_VERSION.zip"
PID_FILE="$KEYCLOAK_DIR/keycloak.pid"
LOG_FILE="$KEYCLOAK_DIR/keycloak.log"
HTTP_PORT="8081"

echo "==> Postgres (native)"
if ! command -v pg_isready >/dev/null 2>&1; then
	echo "error: postgresql is not installed locally. Install it (e.g. apt-get install postgresql) or use 'make dev-up' where Docker is available." >&2
	exit 1
fi
if ! pg_isready -q; then
	echo "starting local postgresql service..."
	service postgresql start
fi
sudo -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='zonaryos'" | grep -q 1 || \
	sudo -u postgres psql -c "CREATE ROLE zonaryos LOGIN PASSWORD 'zonaryos' SUPERUSER;"
sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='zonaryos'" | grep -q 1 || \
	sudo -u postgres psql -c "CREATE DATABASE zonaryos OWNER zonaryos;"
echo "postgres ready: postgres://zonaryos:zonaryos@localhost:5432/zonaryos"

echo "==> Keycloak (standalone, no Docker)"
mkdir -p "$KEYCLOAK_DIR"
if [ ! -d "$KEYCLOAK_HOME" ]; then
	if [ ! -f "$KEYCLOAK_ZIP" ]; then
		echo "downloading Keycloak $KEYCLOAK_VERSION from GitHub Releases..."
		curl -sS -L -o "$KEYCLOAK_ZIP" "$KEYCLOAK_URL"
	fi
	echo "extracting..."
	unzip -q -o "$KEYCLOAK_ZIP" -d "$KEYCLOAK_DIR"
fi

mkdir -p "$KEYCLOAK_HOME/data/import"
cp "$REPO_ROOT/deploy/keycloak/zonaryos-realm.json" "$KEYCLOAK_HOME/data/import/"

if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
	echo "keycloak already running (pid $(cat "$PID_FILE"))"
else
	echo "starting keycloak on :$HTTP_PORT (log: $LOG_FILE)..."
	(
		cd "$KEYCLOAK_HOME"
		export KC_BOOTSTRAP_ADMIN_USERNAME=admin
		export KC_BOOTSTRAP_ADMIN_PASSWORD=admin
		nohup ./bin/kc.sh start-dev --import-realm --http-port="$HTTP_PORT" >"$LOG_FILE" 2>&1 &
		echo $! >"$PID_FILE"
	)

	echo "waiting for Keycloak to become ready..."
	for _ in $(seq 1 60); do
		if curl -sS -o /dev/null "http://localhost:$HTTP_PORT/realms/zonaryos/.well-known/openid-configuration"; then
			echo "keycloak ready: http://localhost:$HTTP_PORT/realms/zonaryos"
			exit 0
		fi
		sleep 2
	done
	echo "error: keycloak did not become ready within the timeout - check $LOG_FILE" >&2
	exit 1
fi
