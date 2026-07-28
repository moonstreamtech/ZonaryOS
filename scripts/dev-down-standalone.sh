#!/usr/bin/env bash
# Stops what scripts/dev-up-standalone.sh started. Postgres is left
# running (it's a shared system service, same as a native install would
# be outside this repo's control) - only the Keycloak process this repo
# started is stopped.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PID_FILE="$REPO_ROOT/.zonaryos/keycloak-standalone/keycloak.pid"

if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
	echo "stopping keycloak (pid $(cat "$PID_FILE"))..."
	kill "$(cat "$PID_FILE")"
	rm -f "$PID_FILE"
else
	echo "keycloak is not running"
fi
