#!/usr/bin/env bash
# Copyright (c) ZonaryOS. All rights reserved.
# Use of this source code is governed by the license found in the LICENSE
# file in the root of this repository (draft, pending legal review - see
# docs/OPEN_POINTS.md item 20).

# CI Checklist item 12, "E2E Smoke Test". Exercises the critical path
# against a real, already-running stack (real Postgres, real Keycloak,
# real backend, real frontend - nothing mocked): login (a real Keycloak-
# issued bearer token, verified by the real internal/identity.Verifier)
# and the core transaction, wizard -> firm creation -> add stock -> sell.
#
# This is the exact curl sequence every prior PR (#7-#10) already used for
# its own manual E2E verification against the standalone dev stack (see
# docs/DEVELOPMENT.md and each PR's description) - translated into an
# automated, asserting script instead of a human reading terminal output,
# not a new bring-up or verification method.
#
# Known scope boundary, not silently substituted: this does not drive a
# headless browser through Keycloak's interactive login form (that would
# need Playwright + a browser download in CI, a further infra addition).
# "Login" here means obtaining a real token from the real Keycloak server
# via its Direct Access Grant flow - the same local-dev-only convenience
# grant docs/DEVELOPMENT.md already documents on the realm - and verifying
# the frontend's own /api/auth/login redirects to that same real Keycloak
# issuer. The full interactive PKCE redirect dance through a browser UI is
# not exercised here.
set -euo pipefail

BACKEND_URL="${BACKEND_URL:-http://localhost:8080}"
FRONTEND_URL="${FRONTEND_URL:-http://localhost:3000}"
KEYCLOAK_ISSUER_URL="${KEYCLOAK_ISSUER_URL:-http://localhost:8081/realms/zonaryos}"
KEYCLOAK_CLIENT_ID="${KEYCLOAK_CLIENT_ID:-zonaryos-web}"
KEYCLOAK_USERNAME="${KEYCLOAK_USERNAME:-dev@zonaryos.local}"
KEYCLOAK_PASSWORD="${KEYCLOAK_PASSWORD:-zonaryos-dev}"

log() { echo "==> $*"; }
fail() { echo "E2E SMOKE TEST FAILED: $*" >&2; exit 1; }

# --- Login: obtain a real, Keycloak-issued bearer token -----------------

log "login: requesting a token from the real Keycloak server"
TOKEN_RESPONSE=$(curl -sS -X POST "$KEYCLOAK_ISSUER_URL/protocol/openid-connect/token" \
    -d "grant_type=password" -d "client_id=$KEYCLOAK_CLIENT_ID" \
    -d "username=$KEYCLOAK_USERNAME" -d "password=$KEYCLOAK_PASSWORD")
TOKEN=$(echo "$TOKEN_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin).get('access_token', ''))")
[ -n "$TOKEN" ] || fail "did not receive an access_token from Keycloak: $TOKEN_RESPONSE"
log "login: got a real access token"

log "login: confirming the frontend's own login redirect points at the same real Keycloak issuer"
LOGIN_REDIRECT=$(curl -sS -o /dev/null -D - "$FRONTEND_URL/api/auth/login" | tr -d '\r' | grep -i '^location:' || true)
echo "$LOGIN_REDIRECT" | grep -qi "keycloak\|realms" || fail "frontend /api/auth/login did not redirect toward a Keycloak realm: $LOGIN_REDIRECT"
log "login: frontend -> Keycloak redirect wiring confirmed ($LOGIN_REDIRECT)"

auth() { curl -sS -H "Authorization: Bearer $TOKEN" "$@"; }

# --- Core transaction: wizard -> firm creation -> add stock -> sell -----

log "wizard: reading the root question node"
ROOT_NODE=$(auth "$BACKEND_URL/api/wizard/nodes/root")
echo "$ROOT_NODE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['kind'] == 'question', d" \
    || fail "expected the wizard root node to be a question: $ROOT_NODE"

FIRM_NAME="E2E Smoke Firm $(date +%s)"
log "wizard: answering 'no' to create a default firm ('$FIRM_NAME')"
ANSWER_RESPONSE=$(auth -X POST "$BACKEND_URL/api/wizard/nodes/root/answer" \
    -H "Content-Type: application/json" \
    -d "{\"answer\":\"no\",\"firmName\":\"$FIRM_NAME\"}")
FIRM_ID=$(echo "$ANSWER_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['result']['firmId'])")
[ -n "$FIRM_ID" ] || fail "did not get a firmId back from the wizard answer: $ANSWER_RESPONSE"
log "firm created: $FIRM_ID"

log "resolving the stock_to_sale workflow definition"
DEFINITION_RESPONSE=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/workflow-definitions?key=stock_to_sale")
DEFINITION_ID=$(echo "$DEFINITION_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['definitionId'])")
[ -n "$DEFINITION_ID" ] || fail "did not resolve the stock_to_sale definition: $DEFINITION_RESPONSE"

log "add stock: creating a workflow instance"
INSTANCE_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$FIRM_ID/workflow-definitions/$DEFINITION_ID/instances" \
    -H "Content-Type: application/json" \
    -d '{"payload":{"item":"E2E Smoke Widget","quantity":1}}')
INSTANCE_ID=$(echo "$INSTANCE_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['instanceId'])")
[ -n "$INSTANCE_ID" ] || fail "did not get an instanceId back from add-stock: $INSTANCE_RESPONSE"
echo "$INSTANCE_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['state']['key'] == 'in_stock', d" \
    || fail "expected the new instance's state to be in_stock: $INSTANCE_RESPONSE"
log "stock added: instance $INSTANCE_ID, state in_stock"

log "sell: executing the record_sale transition"
SALE_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$FIRM_ID/workflow-instances/$INSTANCE_ID/transitions/record_sale" \
    -H "Content-Type: application/json" -d '{}')
echo "$SALE_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['state']['key'] == 'sold', d" \
    || fail "expected the instance's state to be sold after record_sale: $SALE_RESPONSE"
log "sale recorded: instance $INSTANCE_ID is now sold"

log "bonus: confirming the audit trail (PR 8) recorded this transaction"
AUDIT_LOG=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/audit-log")
echo "$AUDIT_LOG" | python3 -c "
import sys, json
entries = json.load(sys.stdin)
actions = {(e['entityType'], e['action']) for e in entries}
assert ('firm', 'create') in actions, entries
assert ('workflow_instance', 'create') in actions, entries
assert ('workflow_instance', 'record_sale') in actions, entries
" || fail "expected firm-create/instance-create/record_sale entries in the audit log: $AUDIT_LOG"
log "audit trail confirmed"

# --- UI-path add stock, firm switch, and audit log view (item 39 / 3 / 4) ---
#
# Everything above exercises the Go backend directly. This section instead
# drives the frontend's own Next.js proxy routes (app/api/stock/create,
# app/api/firm/switch, app/api/audit-log/[firmId]) the real browser UI
# calls - same cookie-to-Bearer-token pattern those routes document, using
# TOKEN as the zonaryos_session cookie value (see
# src/app/api/auth/callback/route.ts: the cookie *is* the raw access
# token, nothing translated in between).

uiAuth() { curl -sS -H "Cookie: zonaryos_session=$TOKEN" "$@"; }

log "add stock (UI path): POSTing through the frontend's own proxy route, not the backend directly"
UI_INSTANCE_RESPONSE=$(uiAuth -X POST "$FRONTEND_URL/api/stock/create" \
    -H "Content-Type: application/json" \
    -d "{\"firmId\":\"$FIRM_ID\",\"definitionId\":\"$DEFINITION_ID\",\"payload\":{\"item\":\"E2E UI-Path Widget\",\"quantity\":3}}")
echo "$UI_INSTANCE_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['state']['key'] == 'in_stock', d" \
    || fail "expected the UI-path instance's state to be in_stock: $UI_INSTANCE_RESPONSE"
log "add stock (UI path) confirmed"

SECOND_FIRM_NAME="E2E Smoke Firm 2 $(date +%s)"
log "wizard: creating a second firm for the same user ('$SECOND_FIRM_NAME'), to exercise the firm switcher"
SECOND_ANSWER_RESPONSE=$(auth -X POST "$BACKEND_URL/api/wizard/nodes/root/answer" \
    -H "Content-Type: application/json" \
    -d "{\"answer\":\"no\",\"firmName\":\"$SECOND_FIRM_NAME\"}")
SECOND_FIRM_ID=$(echo "$SECOND_ANSWER_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['result']['firmId'])")
[ -n "$SECOND_FIRM_ID" ] || fail "did not get a firmId back from the second wizard answer: $SECOND_ANSWER_RESPONSE"
log "second firm created: $SECOND_FIRM_ID"

log "firm switch (UI path): POSTing to the frontend's firm-switch proxy route"
SWITCH_RESPONSE=$(uiAuth -X POST "$FRONTEND_URL/api/firm/switch" \
    -H "Content-Type: application/json" \
    -d "{\"firmId\":\"$SECOND_FIRM_ID\"}")
echo "$SWITCH_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d.get('ok') is True, d" \
    || fail "expected the firm switch to succeed: $SWITCH_RESPONSE"
log "firm switch confirmed"

log "audit log (UI path): confirming the frontend's own proxy route surfaces correctly-attributed entries after a write"
UI_AUDIT_LOG=$(uiAuth "$FRONTEND_URL/api/audit-log/$FIRM_ID")
echo "$UI_AUDIT_LOG" | python3 -c "
import sys, json
entries = json.load(sys.stdin)
matches = [e for e in entries if e['entityType'] == 'workflow_instance' and e['action'] == 'create' and e['changes'].get('payload', {}).get('item') == 'E2E UI-Path Widget']
assert matches, entries
assert matches[0]['userEmail'] == '$KEYCLOAK_USERNAME', matches[0]
" || fail "expected the UI-path add-stock entry to appear in the audit log, correctly attributed to $KEYCLOAK_USERNAME: $UI_AUDIT_LOG"
log "audit log (UI path) confirmed, correctly attributed"

echo ""
echo "E2E SMOKE TEST PASSED: login + wizard -> firm creation -> add stock -> sell, plus UI-path add stock, firm switch, and audit log view, all against a real stack"
