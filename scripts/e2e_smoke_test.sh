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
# drives the frontend's own Next.js proxy routes (app/api/workflow/instances,
# app/api/firm/switch, app/api/audit-log/[firmId]) the real browser UI
# calls - same cookie-to-Bearer-token pattern those routes document, using
# TOKEN as the zonaryos_session cookie value (see
# src/app/api/auth/callback/route.ts: the cookie *is* the raw access
# token, nothing translated in between). app/api/workflow/instances is the
# generic create-instance proxy route (replaced the stock-specific
# app/api/stock/create when the frontend's workflow UI was genericized) -
# it works for any workflow definition, not just stock_to_sale, which is
# exactly what's being exercised here.

uiAuth() { curl -sS -H "Cookie: zonaryos_session=$TOKEN" "$@"; }

log "add stock (UI path): POSTing through the frontend's own generic proxy route, not the backend directly"
UI_INSTANCE_RESPONSE=$(uiAuth -X POST "$FRONTEND_URL/api/workflow/instances" \
    -H "Content-Type: application/json" \
    -d "{\"firmId\":\"$FIRM_ID\",\"definitionId\":\"$DEFINITION_ID\",\"payload\":{\"item\":\"E2E UI-Path Widget\",\"quantity\":3}}")
echo "$UI_INSTANCE_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['state']['key'] == 'in_stock', d" \
    || fail "expected the UI-path instance's state to be in_stock: $UI_INSTANCE_RESPONSE"
log "add stock (UI path) confirmed"

log "workflows list (generic path): confirming GET .../workflow-definitions with no ?key= lists stock_to_sale"
DEFINITIONS_LIST=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/workflow-definitions")
echo "$DEFINITIONS_LIST" | python3 -c "
import sys, json
defs = json.load(sys.stdin)
assert isinstance(defs, list), defs
matches = [d for d in defs if d['key'] == 'stock_to_sale']
assert matches, defs
assert matches[0]['createPermissionKey'], matches[0]
" || fail "expected the no-key GET to list stock_to_sale with a createPermissionKey: $DEFINITIONS_LIST"
log "workflows list (generic path) confirmed"

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

# --- Define a second, structurally different workflow through the real
# API, and confirm the generic frontend actually renders it -------------
#
# Everything genericness-related so far (this batch's own vitest/Go
# integration tests) proves the mechanism works against a synthetic Go
# fixture (workflow_integration_test.go's purchaseOrderSpec) or in
# isolated unit tests. This section is the real end-to-end proof: define
# a workflow with a completely different shape than stock_to_sale
# (three states/two transitions vs. two states/one transition, and a
# "vendor" payload field instead of "item"/"quantity") through the real,
# owner-gated POST .../workflow-definitions endpoint (via the frontend's
# own proxy route, same UI path a real builder submission takes), then
# confirm the generic /workflows/[key] page (built without any
# stock_to_sale-specific code) actually renders it - not a mock, not a
# unit test, a real curl against the running frontend.

log "define workflow (UI path): POSTing a structurally different purchase_order-shaped spec"
DEFINE_RESPONSE=$(uiAuth -X POST "$FRONTEND_URL/api/workflow/definitions" \
    -H "Content-Type: application/json" \
    -d '{
      "firmId":"'"$FIRM_ID"'",
      "spec":{
        "key":"purchase_order",
        "name":"Purchase Order",
        "createPermission":{"key":"workflow.purchase_order.create","description":"Create a purchase order."},
        "states":[
          {"key":"draft","name":"Draft","isInitial":true},
          {"key":"approved","name":"Approved"},
          {"key":"received","name":"Received","isTerminal":true}
        ],
        "transitions":[
          {"fromStateKey":"draft","toStateKey":"approved","actionKey":"approve","name":"Approve","permission":{"key":"workflow.purchase_order.approve","description":"Approve a draft purchase order."}},
          {"fromStateKey":"approved","toStateKey":"received","actionKey":"receive","name":"Receive","permission":{"key":"workflow.purchase_order.receive","description":"Mark an approved purchase order as received."}}
        ]
      }
    }')
PO_DEFINITION_ID=$(echo "$DEFINE_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['definitionId'])")
[ -n "$PO_DEFINITION_ID" ] || fail "did not get a definitionId back from defining purchase_order: $DEFINE_RESPONSE"
log "purchase_order workflow defined: $PO_DEFINITION_ID"

log "add a purchase_order instance (UI path), through the exact same generic create route stock_to_sale uses"
PO_INSTANCE_RESPONSE=$(uiAuth -X POST "$FRONTEND_URL/api/workflow/instances" \
    -H "Content-Type: application/json" \
    -d "{\"firmId\":\"$FIRM_ID\",\"definitionId\":\"$PO_DEFINITION_ID\",\"payload\":{\"vendor\":\"Acme Supply Co\"}}")
echo "$PO_INSTANCE_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['state']['key'] == 'draft', d" \
    || fail "expected the new purchase_order instance's state to be draft: $PO_INSTANCE_RESPONSE"
log "purchase_order instance added, state draft"

log "workflows list (UI path): confirming the frontend's own /workflows page lists Purchase Order alongside Stock In -> Sale"
WORKFLOWS_LIST_HTML=$(curl -sS -b "zonaryos_session=$TOKEN" "$FRONTEND_URL/en/workflows")
echo "$WORKFLOWS_LIST_HTML" | grep -q "Purchase Order" \
    || fail "expected the frontend's /workflows list page to render the newly-defined Purchase Order definition"
log "workflows list (UI path) confirmed"

log "generic workflow view (UI path): confirming /workflows/purchase_order renders through the SAME generic component tree as /stock, with zero purchase_order-specific frontend code"
PO_VIEW_HTML=$(curl -sS -b "zonaryos_session=$TOKEN" "$FRONTEND_URL/en/workflows/purchase_order")
echo "$PO_VIEW_HTML" | grep -q "Purchase Order" \
    || fail "expected /workflows/purchase_order to render the definition's own name"
echo "$PO_VIEW_HTML" | grep -q "Acme Supply Co" \
    || fail "expected /workflows/purchase_order to render the new instance's payload (vendor: Acme Supply Co) via the generic formatPayload rendering"
echo "$PO_VIEW_HTML" | grep -q "Approve" \
    || fail "expected /workflows/purchase_order to render the 'Approve' action button, labeled from the backend's own AvailableAction.Name"
log "generic workflow view (UI path) confirmed - a second, structurally different workflow renders correctly with no new frontend code"

# --- Customer Pipeline: the wizard's second default-seeded workflow -----
#
# internal/wizard.CreateDefaultFirm now seeds Customer Pipeline alongside
# Stock In -> Sale for every new firm (this batch) - FIRM_ID (created
# above) already has both. Walks a lead through create -> qualify ->
# convert, the exact same generic engine path stock_to_sale exercised
# above, then confirms the dashboard's new counts-by-state overview
# (item 2) reflects it, both at the backend endpoint and through the
# real frontend page.

log "resolving the customer_pipeline workflow definition"
CRM_DEFINITION_RESPONSE=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/workflow-definitions?key=customer_pipeline")
CRM_DEFINITION_ID=$(echo "$CRM_DEFINITION_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['definitionId'])")
[ -n "$CRM_DEFINITION_ID" ] || fail "did not resolve the customer_pipeline definition: $CRM_DEFINITION_RESPONSE"
log "customer_pipeline resolved: $CRM_DEFINITION_ID"

log "create lead: creating a customer_pipeline instance"
LEAD_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$FIRM_ID/workflow-definitions/$CRM_DEFINITION_ID/instances" \
    -H "Content-Type: application/json" \
    -d '{"payload":{"name":"E2E Smoke Lead","contact":"lead@example.com"}}')
LEAD_ID=$(echo "$LEAD_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['instanceId'])")
[ -n "$LEAD_ID" ] || fail "did not get an instanceId back from create-lead: $LEAD_RESPONSE"
echo "$LEAD_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['state']['key'] == 'lead', d" \
    || fail "expected the new lead's state to be lead: $LEAD_RESPONSE"
log "lead created: instance $LEAD_ID, state lead"

log "qualify: executing the qualify transition"
QUALIFY_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$FIRM_ID/workflow-instances/$LEAD_ID/transitions/qualify" \
    -H "Content-Type: application/json" -d '{}')
echo "$QUALIFY_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['state']['key'] == 'qualified', d" \
    || fail "expected the lead's state to be qualified after qualify: $QUALIFY_RESPONSE"
log "lead qualified: instance $LEAD_ID is now qualified"

log "convert: executing the convert transition"
CONVERT_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$FIRM_ID/workflow-instances/$LEAD_ID/transitions/convert" \
    -H "Content-Type: application/json" -d '{}')
echo "$CONVERT_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['state']['key'] == 'customer', d" \
    || fail "expected the lead's state to be customer after convert: $CONVERT_RESPONSE"
log "lead converted: instance $LEAD_ID is now a customer"

log "dashboard overview (backend path): confirming GET .../workflow-instance-counts reports both workflows correctly"
COUNTS_RESPONSE=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/workflow-instance-counts")
echo "$COUNTS_RESPONSE" | python3 -c "
import sys, json
defs = json.load(sys.stdin)
assert isinstance(defs, list), defs
stock = next((d for d in defs if d['key'] == 'stock_to_sale'), None)
assert stock, defs
stock_counts = {c['stateKey']: c['count'] for c in stock['counts']}
assert stock_counts.get('sold', 0) >= 1, stock_counts
crm = next((d for d in defs if d['key'] == 'customer_pipeline'), None)
assert crm, defs
crm_counts = {c['stateKey']: c['count'] for c in crm['counts']}
assert crm_counts.get('customer', 0) >= 1, crm_counts
# 'lost' must be reported even though nothing reached it yet - the
# LEFT JOIN's whole point (see InstanceCountsByDefinition's doc comment).
assert 'lost' in crm_counts, crm_counts
" || fail "expected workflow-instance-counts to report accurate per-state counts for both workflows: $COUNTS_RESPONSE"
log "dashboard overview (backend path) confirmed"

log "dashboard overview (UI path): confirming the frontend's own dashboard page renders both workflows' overview cards"
DASHBOARD_HTML=$(curl -sS -b "zonaryos_session=$TOKEN" "$FRONTEND_URL/en")
echo "$DASHBOARD_HTML" | grep -q "Stock In -> Sale\|Stock In -&gt; Sale" \
    || fail "expected the dashboard to render a Stock In -> Sale overview card"
echo "$DASHBOARD_HTML" | grep -q "Customer Pipeline" \
    || fail "expected the dashboard to render a Customer Pipeline overview card"
log "dashboard overview (UI path) confirmed"

log "quick create (UI path): creating a second lead directly through the dashboard's generic create route, same as any other workflow"
QUICK_CREATE_RESPONSE=$(uiAuth -X POST "$FRONTEND_URL/api/workflow/instances" \
    -H "Content-Type: application/json" \
    -d "{\"firmId\":\"$FIRM_ID\",\"definitionId\":\"$CRM_DEFINITION_ID\",\"payload\":{\"name\":\"E2E Quick-Create Lead\"}}")
echo "$QUICK_CREATE_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['state']['key'] == 'lead', d" \
    || fail "expected the quick-created lead's state to be lead: $QUICK_CREATE_RESPONSE"
log "quick create (UI path) confirmed"

log "global search (UI path): confirming /search finds the converted lead, grouped under Customer Pipeline"
SEARCH_HTML=$(curl -sS -b "zonaryos_session=$TOKEN" "$FRONTEND_URL/en/search?q=E2E%20Smoke%20Lead")
echo "$SEARCH_HTML" | grep -q "Customer Pipeline" \
    || fail "expected /search?q=E2E Smoke Lead to render a Customer Pipeline results group"
echo "$SEARCH_HTML" | grep -q "E2E Smoke Lead" \
    || fail "expected /search?q=E2E Smoke Lead to render the matching lead's payload"
log "global search (UI path) confirmed"

# --- Invites: self-registration -> invite -> accept, a second real user
# --- joins the firm, with no email infrastructure of any kind -----------
#
# Proves the whole flow this batch added, curl-driven end to end - not a
# headless browser (Playwright would be a further infra addition, same
# scope boundary docs/DEVELOPMENT.md's E2E section already documents for
# login above), but a real interactive registration form submission
# against the real Keycloak server, same as the manual verification this
# batch's own investigation already did once by hand (see
# docs/DEVELOPMENT.md's "Invites" section).

log "invites: creating a non-owner role to invite someone into"
ROLE_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$FIRM_ID/roles" \
    -H "Content-Type: application/json" -d '{"key":"e2e-invitee","name":"E2E Invitee"}')
ROLE_ID=$(echo "$ROLE_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['roleId'])")
[ -n "$ROLE_ID" ] || fail "did not get a roleId back from role creation: $ROLE_RESPONSE"

log "invites: the owner generates an invite for that role"
INVITE_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$FIRM_ID/invites" \
    -H "Content-Type: application/json" -d "{\"roleId\":\"$ROLE_ID\"}")
INVITE_TOKEN=$(echo "$INVITE_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['token'])")
[ -n "$INVITE_TOKEN" ] || fail "did not get a token back from invite generation: $INVITE_RESPONSE"
log "invite generated, link would be $FRONTEND_URL/en/invite/$INVITE_TOKEN"

log "invites: the public, unauthenticated lookup endpoint resolves it"
LOOKUP_RESPONSE=$(curl -sS "$BACKEND_URL/api/invites/$INVITE_TOKEN")
echo "$LOOKUP_RESPONSE" | python3 -c "
import sys, json
d = json.load(sys.stdin)
assert d['status'] == 'pending' and not d['expired'], d
" || fail "expected the freshly generated invite to look up as pending/unexpired: $LOOKUP_RESPONSE"

log "self-registration: creating a second real Keycloak user through the interactive registration form"
COOKIEJAR=$(mktemp)
REG_HEADERS=$(mktemp)
CODE_VERIFIER=$(python3 -c "import secrets, base64; print(base64.urlsafe_b64encode(secrets.token_bytes(32)).rstrip(b'=').decode())")
CODE_CHALLENGE=$(python3 -c "
import hashlib, base64
print(base64.urlsafe_b64encode(hashlib.sha256('$CODE_VERIFIER'.encode()).digest()).rstrip(b'=').decode())
")
INVITEE_EMAIL="e2e-invitee-$(date +%s)@example.com"
INVITEE_PASSWORD="E2eInviteePass123!"

REG_PAGE=$(curl -sS -c "$COOKIEJAR" "$KEYCLOAK_ISSUER_URL/protocol/openid-connect/registrations?client_id=$KEYCLOAK_CLIENT_ID&response_type=code&redirect_uri=$FRONTEND_URL/api/auth/callback&scope=openid&state=e2e-invite&code_challenge=$CODE_CHALLENGE&code_challenge_method=S256")
REG_ACTION=$(echo "$REG_PAGE" | grep -o 'action="[^"]*"' | head -1 | sed 's/action="//; s/"$//; s/&amp;/\&/g')
[ -n "$REG_ACTION" ] || fail "could not find the Keycloak registration form's action URL - is registrationAllowed still true in deploy/keycloak/zonaryos-realm.json?"

curl -sS -b "$COOKIEJAR" -c "$COOKIEJAR" -o /dev/null -D "$REG_HEADERS" "$REG_ACTION" \
    --data-urlencode "firstName=E2E" \
    --data-urlencode "lastName=Invitee" \
    --data-urlencode "email=$INVITEE_EMAIL" \
    --data-urlencode "password=$INVITEE_PASSWORD" \
    --data-urlencode "password-confirm=$INVITEE_PASSWORD"
grep -qi '^location:.*code=' "$REG_HEADERS" || fail "self-registration did not redirect back with an authorization code (no email-verification interstitial expected, since verifyEmail is off): $(cat "$REG_HEADERS")"
log "self-registration succeeded ($INVITEE_EMAIL), no email verification required"
rm -f "$COOKIEJAR" "$REG_HEADERS"

log "invitee login: obtaining a real token for the newly self-registered user via Direct Access Grant"
INVITEE_TOKEN_RESPONSE=$(curl -sS -X POST "$KEYCLOAK_ISSUER_URL/protocol/openid-connect/token" \
    -d "grant_type=password" -d "client_id=$KEYCLOAK_CLIENT_ID" \
    -d "username=$INVITEE_EMAIL" -d "password=$INVITEE_PASSWORD")
INVITEE_TOKEN=$(echo "$INVITEE_TOKEN_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin).get('access_token', ''))")
[ -n "$INVITEE_TOKEN" ] || fail "did not receive an access_token for the self-registered invitee: $INVITEE_TOKEN_RESPONSE"

log "accept: the invitee accepts the invite, with no prior membership in any firm"
ACCEPT_RESPONSE=$(curl -sS -X POST -H "Authorization: Bearer $INVITEE_TOKEN" "$BACKEND_URL/api/invites/$INVITE_TOKEN/accept")
echo "$ACCEPT_RESPONSE" | python3 -c "
import sys, json
d = json.load(sys.stdin)
assert d['firmId'] == '$FIRM_ID' and d['roleId'] == '$ROLE_ID', d
" || fail "expected the accept response to report firmId=$FIRM_ID roleId=$ROLE_ID: $ACCEPT_RESPONSE"
log "invite accepted: $INVITEE_EMAIL is now a member of $FIRM_ID as role $ROLE_ID"

log "accept: confirming single-use enforcement - a second accept attempt on the same token is rejected"
SECOND_ACCEPT_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $INVITEE_TOKEN" "$BACKEND_URL/api/invites/$INVITE_TOKEN/accept")
[ "$SECOND_ACCEPT_STATUS" = "409" ] || fail "expected 409 (already used) on a second accept attempt, got $SECOND_ACCEPT_STATUS"
log "single-use enforcement confirmed (second accept -> 409)"

log "members: confirming the roster now shows two people"
MEMBERS_RESPONSE=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/members")
MEMBER_COUNT=$(echo "$MEMBERS_RESPONSE" | python3 -c "import sys, json; print(len(json.load(sys.stdin)))")
[ "$MEMBER_COUNT" = "2" ] || fail "expected 2 members after the invite was accepted, got $MEMBER_COUNT: $MEMBERS_RESPONSE"
log "invites: full self-registration -> invite -> accept flow verified end-to-end ($MEMBER_COUNT members now in the firm)"

# --- Session refresh (Open Points item 41): a real, expired access token
# --- is silently refreshed by src/proxy.ts, no re-login prompt ----------
#
# Genuine proof, not "login works then immediately calls again": this
# section makes a real access token issued by the real Keycloak server
# actually expire, then proves the frontend transparently mints a new one
# via the refresh_token grant and serves the original request anyway.
#
# A full, honest ~5-minute sleep (Keycloak's real default
# accessTokenLifespan) is impractical for a CI job that otherwise runs in
# well under a minute, so this uses Keycloak's own Admin REST API (the
# KC_BOOTSTRAP_ADMIN_USERNAME/PASSWORD docker-compose.yml already sets for
# the dev Keycloak container) to temporarily override the realm's
# accessTokenLifespan to a few seconds, obtains a real token under that
# override, and genuinely waits (a real `sleep`, no clock mocking) past
# its real expiry - the same server-side validation path a full 5-minute
# wait would exercise, just compressed. The realm override is restored
# (via a trap, so it's undone even if an assertion below fails) before
# the script exits, so this leaves the realm's config exactly as every
# other section of this test found it.

ORIGINAL_REALM_JSON=$(mktemp)
restore_realm_config() {
    if [ -s "$ORIGINAL_REALM_JSON" ] && [ -n "${ADMIN_TOKEN:-}" ]; then
        curl -sS -X PUT "${KEYCLOAK_BASE_URL:-http://localhost:8081}/admin/realms/zonaryos" \
            -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
            --data-binary "@$ORIGINAL_REALM_JSON" >/dev/null || true
    fi
    rm -f "$ORIGINAL_REALM_JSON"
}
trap restore_realm_config EXIT

KEYCLOAK_BASE_URL="${KEYCLOAK_BASE_URL:-http://localhost:8081}"
KEYCLOAK_ADMIN_USERNAME="${KEYCLOAK_ADMIN_USERNAME:-admin}"
KEYCLOAK_ADMIN_PASSWORD="${KEYCLOAK_ADMIN_PASSWORD:-admin}"

log "session refresh: obtaining a Keycloak admin token to temporarily shorten the access-token lifespan"
ADMIN_TOKEN_RESPONSE=$(curl -sS -X POST "$KEYCLOAK_BASE_URL/realms/master/protocol/openid-connect/token" \
    -d "grant_type=password" -d "client_id=admin-cli" \
    -d "username=$KEYCLOAK_ADMIN_USERNAME" -d "password=$KEYCLOAK_ADMIN_PASSWORD")
ADMIN_TOKEN=$(echo "$ADMIN_TOKEN_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin).get('access_token', ''))")
[ -n "$ADMIN_TOKEN" ] || fail "did not receive an admin access_token from Keycloak's master realm: $ADMIN_TOKEN_RESPONSE"

curl -sS "$KEYCLOAK_BASE_URL/admin/realms/zonaryos" -H "Authorization: Bearer $ADMIN_TOKEN" > "$ORIGINAL_REALM_JSON"
python3 -c "import json; json.load(open('$ORIGINAL_REALM_JSON'))" \
    || fail "did not receive a parseable realm representation from the admin API: $(cat "$ORIGINAL_REALM_JSON")"

SHORT_LIFESPAN_JSON=$(mktemp)
python3 -c "
import json
d = json.load(open('$ORIGINAL_REALM_JSON'))
d['accessTokenLifespan'] = 5
json.dump(d, open('$SHORT_LIFESPAN_JSON', 'w'))
"
log "session refresh: setting accessTokenLifespan=5s on the zonaryos realm (test-only override, restored on exit)"
SHORT_LIFESPAN_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' -X PUT "$KEYCLOAK_BASE_URL/admin/realms/zonaryos" \
    -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
    --data-binary "@$SHORT_LIFESPAN_JSON")
rm -f "$SHORT_LIFESPAN_JSON"
[ "$SHORT_LIFESPAN_STATUS" = "204" ] || fail "expected 204 setting accessTokenLifespan, got $SHORT_LIFESPAN_STATUS"

log "session refresh: logging in again to get a token that actually carries the 5s lifespan"
SHORT_TOKEN_RESPONSE=$(curl -sS -X POST "$KEYCLOAK_ISSUER_URL/protocol/openid-connect/token" \
    -d "grant_type=password" -d "client_id=$KEYCLOAK_CLIENT_ID" \
    -d "username=$KEYCLOAK_USERNAME" -d "password=$KEYCLOAK_PASSWORD")
SHORT_ACCESS_TOKEN=$(echo "$SHORT_TOKEN_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin).get('access_token', ''))")
SHORT_REFRESH_TOKEN=$(echo "$SHORT_TOKEN_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin).get('refresh_token', ''))")
SHORT_EXPIRES_IN=$(echo "$SHORT_TOKEN_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin).get('expires_in', ''))")
[ -n "$SHORT_ACCESS_TOKEN" ] && [ -n "$SHORT_REFRESH_TOKEN" ] || fail "did not receive both an access_token and refresh_token under the shortened lifespan: $SHORT_TOKEN_RESPONSE"
[ "$SHORT_EXPIRES_IN" = "5" ] || fail "expected the token response to report expires_in=5 confirming the realm override actually took effect, got $SHORT_EXPIRES_IN"
log "session refresh: got a real access token that genuinely expires in 5s (refresh token is long-lived as usual)"

log "session refresh: sleeping 8s (a real wait, past the token's real 5s expiry - not simulated)"
sleep 8

log "session refresh: confirming the now-actually-expired access token is genuinely rejected by the real backend directly (proves this isn't a false expiry)"
DIRECT_STATUS_WITH_EXPIRED=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $SHORT_ACCESS_TOKEN" "$BACKEND_URL/api/me")
[ "$DIRECT_STATUS_WITH_EXPIRED" = "401" ] || fail "expected the backend to reject the genuinely-expired access token directly with 401, got $DIRECT_STATUS_WITH_EXPIRED - the token isn't actually expired, this proof would be meaningless"
log "session refresh: confirmed the access token is genuinely expired (backend rejects it directly with 401)"

REFRESH_HEADERS=$(mktemp)
log "session refresh: hitting the frontend dashboard with the expired access-token cookie + a still-valid refresh-token cookie"
REFRESH_PROOF_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' -D "$REFRESH_HEADERS" \
    -b "zonaryos_session=$SHORT_ACCESS_TOKEN; zonaryos_refresh_token=$SHORT_REFRESH_TOKEN" \
    "$FRONTEND_URL/en")
[ "$REFRESH_PROOF_STATUS" = "200" ] || fail "expected the frontend to still render the dashboard (200) via silent refresh despite the expired access token, got $REFRESH_PROOF_STATUS"

NEW_SESSION_COOKIE=$(grep -i '^set-cookie: *zonaryos_session=' "$REFRESH_HEADERS" | sed -E 's/^[Ss]et-[Cc]ookie: *zonaryos_session=([^;]*);.*/\1/' | tr -d '\r')
[ -n "$NEW_SESSION_COOKIE" ] || fail "expected src/proxy.ts to set a fresh zonaryos_session cookie on the response: $(cat "$REFRESH_HEADERS")"
[ "$NEW_SESSION_COOKIE" != "$SHORT_ACCESS_TOKEN" ] || fail "the zonaryos_session cookie was not actually replaced with a new access token"
log "session refresh: proxy.ts issued a brand-new zonaryos_session cookie, different from the expired one"

log "session refresh: confirming the new access token is real and accepted by the backend directly"
DIRECT_STATUS_WITH_NEW=$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $NEW_SESSION_COOKIE" "$BACKEND_URL/api/me")
[ "$DIRECT_STATUS_WITH_NEW" = "200" ] || fail "expected the backend to accept the refreshed access token directly with 200, got $DIRECT_STATUS_WITH_NEW"
log "session refresh: confirmed the refreshed access token is real and valid - the request never fell through to a re-login prompt"
rm -f "$REFRESH_HEADERS"

# --- Terminal case: once the refresh token itself is invalid (revoked
# --- here, standing in for it actually expiring), a real re-login IS
# --- correct - proxy.ts must clear both cookies, not loop or error ------

log "session refresh (terminal case): revoking the refresh token server-side (Keycloak logout), simulating it having actually expired"
curl -sS -o /dev/null -X POST "$KEYCLOAK_ISSUER_URL/protocol/openid-connect/logout" \
    -d "client_id=$KEYCLOAK_CLIENT_ID" -d "refresh_token=$SHORT_REFRESH_TOKEN"

TERMINAL_HEADERS=$(mktemp)
# /en itself is the public landing/login page (it renders a signed-out
# state at 200 rather than redirecting - it *is* where an unauthenticated
# visitor is supposed to land), so this hits /en/stock instead - a firm-
# scoped page that goes through requireFirmContext (src/lib/firmContext.ts)
# and calls redirect() whenever there's no valid session, same as every
# other firm-scoped page in this app.
TERMINAL_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' -D "$TERMINAL_HEADERS" \
    -b "zonaryos_session=$SHORT_ACCESS_TOKEN; zonaryos_refresh_token=$SHORT_REFRESH_TOKEN" \
    "$FRONTEND_URL/en/stock")
[ "$TERMINAL_STATUS" = "307" ] || [ "$TERMINAL_STATUS" = "302" ] || fail "expected a redirect (real re-login is correct once the refresh token is actually invalid), got $TERMINAL_STATUS"
grep -qi '^set-cookie: *zonaryos_session=;\|^set-cookie: *zonaryos_session=.*Max-Age=0\|^set-cookie: *zonaryos_session=.*Expires=Thu, 01 Jan 1970' "$TERMINAL_HEADERS" \
    || fail "expected proxy.ts to clear the stale zonaryos_session cookie on the terminal path: $(cat "$TERMINAL_HEADERS")"
log "session refresh (terminal case): confirmed - an invalid refresh token cleanly clears cookies and falls through to the existing login redirect, no loop, no confusing error"
rm -f "$TERMINAL_HEADERS"

restore_realm_config
trap - EXIT
log "session refresh: realm's accessTokenLifespan restored to its original (unset/default) value"

echo ""
echo "E2E SMOKE TEST PASSED: login + wizard -> firm creation -> add stock -> sell, plus UI-path add stock, firm switch, audit log view, a real second workflow defined + rendered through the generic UI, the Customer Pipeline default workflow walked lead -> qualified -> customer, the dashboard's counts-by-state overview, quick-create, global search, a full self-registration -> invite -> accept flow bringing a second real user into the firm with no email infrastructure, and (new) a genuine session-refresh proof - a real Keycloak access token actually expires, gets silently refreshed by src/proxy.ts with no re-login prompt, and the terminal (invalid refresh token) case cleanly falls through to a real login redirect - all against a real stack"
