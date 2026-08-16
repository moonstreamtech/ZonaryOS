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

# fetchPageUntilContains: GETs url (with the given extra curl args, e.g.
# -b "cookie...") up to 5 times, briefly sleeping between attempts, until
# the response body contains needle (a plain grep -F pattern - fixed
# string, not a regex, since every caller's needle is real data like an
# invoice number or a customer name that could itself contain regex
# metacharacters). Prints the last-fetched body to stdout either way,
# same as a plain curl call, so `VAR=$(fetchPageUntilContains ...)`
# drops in wherever `VAR=$(curl ...)` used to be; exits non-zero only if
# needle never showed up.
#
# Exists because a page rendered via SSR immediately after the write it's
# supposed to reflect goes through a genuinely separate request/response
# cycle from the write itself (a fresh HTTP connection to the frontend,
# which itself makes a fresh HTTP connection to the backend) - under
# load, that second round trip can occasionally lose a race with the
# first one's own completion, independent of any caching layer. This
# isn't a substitute for real cache-correctness (a page that were
# actually serving STALE data on every request would still be a bug
# fetchPageUntilContains's retries would mask) - it's tolerance for the
# ordinary "write, then read a moment later" timing every one of this
# script's "(UI path)" checks depends on, the same reasoning a client
# polling for eventual consistency would already need in production.
fetchPageUntilContains() {
    local url="$1" needle="$2"
    shift 2
    local body=""
    for _ in 1 2 3 4 5; do
        body=$(curl -sS "$@" "$url")
        if grep -qF -- "$needle" <<<"$body"; then
            echo "$body"
            return 0
        fi
        sleep 1
    done
    echo "$body"
    return 1
}

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
grep -qi "keycloak\|realms" <<<"$LOGIN_REDIRECT" || fail "frontend /api/auth/login did not redirect toward a Keycloak realm: $LOGIN_REDIRECT"
log "login: frontend -> Keycloak redirect wiring confirmed ($LOGIN_REDIRECT)"

auth() { curl -sS -H "Authorization: Bearer $TOKEN" "$@"; }

# wizard_walk drives the wizard's decision tree (internal/wizard/tree.go)
# from "root" through a sequence of yes/no answers, following each
# response's own "key" to the next node - the same way the frontend's
# WizardClient.tsx does, since the tree is now several questions deep
# (Open Points item 12's wizard content batch) rather than the single
# "do you manufacture?" question this script originally answered in one
# shot. Prints the final answer response (expected to carry "result" once
# the sequence reaches the create-default-firm action).
wizard_walk() {
    local firm_name="$1"; shift
    local node_key="root"
    local response=""
    for ans in "$@"; do
        response=$(auth -X POST "$BACKEND_URL/api/wizard/nodes/$node_key/answer" \
            -H "Content-Type: application/json" \
            -d "{\"answer\":\"$ans\",\"firmName\":\"$firm_name\"}")
        node_key=$(echo "$response" | python3 -c "import sys, json; print(json.load(sys.stdin)['key'])") \
            || fail "wizard_walk: unexpected response answering '$ans' at node '$node_key': $response"
    done
    echo "$response"
}

# --- Core transaction: wizard -> firm creation -> add stock -> sell -----

log "wizard: reading the root question node"
ROOT_NODE=$(auth "$BACKEND_URL/api/wizard/nodes/root")
echo "$ROOT_NODE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['kind'] == 'question', d" \
    || fail "expected the wizard root node to be a question: $ROOT_NODE"

FIRM_NAME="E2E Smoke Firm $(date +%s)"
# sells=yes, tracks inventory=yes, manages CRM=yes, purchases=no (kept
# available below for the manual UI-path purchase_order definition),
# manages tasks=no, manages people=no, manufactures=no -> seeds Stock In ->
# Sale AND Customer Pipeline (and not absence_approval, since people=no),
# matching what the rest of this script assumes FIRM_ID already has.
log "wizard: walking sells=yes/inventory=yes/crm=yes/purchase=no/tasks=no/people=no/manufacture=no to create a default firm ('$FIRM_NAME')"
ANSWER_RESPONSE=$(wizard_walk "$FIRM_NAME" yes yes yes no no no no)
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

log "sell: executing the record_sale transition (with unit_price, so the workflow-to-ledger bridge posts a real journal entry)"
SALE_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$FIRM_ID/workflow-instances/$INSTANCE_ID/transitions/record_sale" \
    -H "Content-Type: application/json" -d '{"payload":{"unit_price":19.99}}')
echo "$SALE_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['state']['key'] == 'sold', d" \
    || fail "expected the instance's state to be sold after record_sale: $SALE_RESPONSE"
log "sale recorded: instance $INSTANCE_ID is now sold"

log "financial management core: confirming record_sale posted a real journal entry via GET .../journal-entries"
JOURNAL_ENTRIES=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/journal-entries")
echo "$JOURNAL_ENTRIES" | python3 -c "
import sys, json
entries = json.load(sys.stdin)
matches = [e for e in entries if e.get('sourceType') == 'workflow_instance' and e.get('sourceId') == '$INSTANCE_ID']
assert len(matches) == 1, entries
entry = matches[0]
lines = {(l['accountCode'], l['side']): l['amount'] for l in entry['lines']}
assert len(lines) == 2, entry
# quantity(1) * unit_price(19.99) = 19.99, both legs.
assert lines[('1100', 'debit')] == '19.9900', entry
assert lines[('4000', 'credit')] == '19.9900', entry
" || fail "expected a balanced journal entry (DR 1100 / CR 4000, 19.9900) posted for instance $INSTANCE_ID: $JOURNAL_ENTRIES"
log "journal entry confirmed: DR Trade Receivables / CR Sales Revenue, 19.9900"

# --- Invoicing, payment tracking, and receivables management: the same
# --- record_sale call above (quantity=1 from add-stock, unit_price=19.99
# --- from record_sale itself, both present in the merged payload) should
# --- have ALSO auto-created a draft invoice via the workflow-to-invoicing
# --- bridge (InvoiceTemplate) - confirm it, then record a payment against
# --- it and confirm it closes to 'paid' with a real DR Cash / CR Trade
# --- Receivables journal entry, per the design brief's own E2E section. -

log "invoicing: confirming record_sale also auto-created a draft invoice via the workflow-to-invoicing bridge"
INVOICES_RESPONSE=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/invoices")
INVOICE_ID=$(echo "$INVOICES_RESPONSE" | python3 -c "
import sys, json
invoices = json.load(sys.stdin)
matches = [i for i in invoices if i.get('sourceWorkflowInstance') == '$INSTANCE_ID']
assert len(matches) == 1, invoices
inv = matches[0]
assert inv['status'] == 'draft', inv
assert inv['total'] == '19.9900', inv
assert inv['invoiceNumber'].startswith('INV-'), inv
print(inv['id'])
")
[ -n "$INVOICE_ID" ] || fail "expected record_sale to auto-create a draft invoice: $INVOICES_RESPONSE"
log "invoice auto-created: $INVOICE_ID (draft, total 19.9900)"

log "invoicing: confirming GET .../invoices/{id} returns the invoice's one line"
INVOICE_DETAIL=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/invoices/$INVOICE_ID")
echo "$INVOICE_DETAIL" | python3 -c "
import sys, json
inv = json.load(sys.stdin)
assert len(inv['lines']) == 1, inv
line = inv['lines'][0]
assert line['description'] == 'Sale of E2E Smoke Widget', line
assert line['quantity'] == '1.0000' and line['unitPrice'] == '19.9900', line
" || fail "expected the invoice's one line to match the sale: $INVOICE_DETAIL"
log "invoice line confirmed"

log "invoicing (UI path): confirming /invoices renders the new invoice"
INVOICE_NUMBER=$(echo "$INVOICE_DETAIL" | python3 -c "import sys, json; print(json.load(sys.stdin)['invoiceNumber'])")
INVOICES_PAGE_HTML=$(fetchPageUntilContains "$FRONTEND_URL/en/invoices" "$INVOICE_NUMBER" -b "zonaryos_session=$TOKEN; zonaryos_active_firm=$FIRM_ID") \
    || fail "expected /invoices to render the new invoice's number"
log "invoicing (UI path) confirmed"

log "invoicing: sending the invoice (draft -> sent) via PATCH"
auth -X PATCH "$BACKEND_URL/api/firms/$FIRM_ID/invoices/$INVOICE_ID" \
    -H "Content-Type: application/json" -d '{"status":"sent"}' >/dev/null

log "invoicing: recording a payment that fully covers the invoice"
PAYMENT_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$FIRM_ID/invoices/$INVOICE_ID/payments" \
    -H "Content-Type: application/json" \
    -d '{"amount":"19.99","paidAt":"2026-01-01T00:00:00Z","method":"bank_transfer","reference":"E2E-PAY-1"}')
echo "$PAYMENT_RESPONSE" | python3 -c "
import sys, json
d = json.load(sys.stdin)
assert d['invoice']['status'] == 'paid', d
" || fail "expected the invoice to auto-close to paid once the payment covers its total: $PAYMENT_RESPONSE"
log "invoicing: payment recorded, invoice closed to paid"

log "invoicing: confirming the payment's DR Cash / CR Trade Receivables journal entry was posted"
CLOSING_JOURNAL=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/journal-entries")
echo "$CLOSING_JOURNAL" | python3 -c "
import sys, json
entries = json.load(sys.stdin)
matches = [e for e in entries if e.get('sourceType') == 'invoice' and e.get('sourceId') == '$INVOICE_ID']
assert len(matches) == 1, entries
entry = matches[0]
lines = {(l['accountCode'], l['side']): l['amount'] for l in entry['lines']}
assert len(lines) == 2, entry
assert lines[('1000', 'debit')] == '19.9900', entry
assert lines[('1100', 'credit')] == '19.9900', entry
" || fail "expected a balanced journal entry (DR 1000 Cash / CR 1100 Trade Receivables, 19.9900) posted for invoice $INVOICE_ID: $CLOSING_JOURNAL"
log "invoicing: payment-closing journal entry confirmed (DR Cash / CR Trade Receivables, 19.9900)"

log "invoicing: confirming GET .../reports/receivables-aging responds with the four fixed buckets"
AGING_RESPONSE=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/reports/receivables-aging")
echo "$AGING_RESPONSE" | python3 -c "
import sys, json
buckets = json.load(sys.stdin)
labels = {b['label'] for b in buckets}
assert labels == {'0-30', '31-60', '61-90', '90+'}, buckets
" || fail "expected the aging report to always list all four fixed buckets: $AGING_RESPONSE"
log "invoicing: receivables aging report confirmed"

log "financials (UI path): confirming /financials?tab=aging renders"
AGING_PAGE_HTML=$(fetchPageUntilContains "$FRONTEND_URL/en/financials?tab=aging" "0-30" -b "zonaryos_session=$TOKEN; zonaryos_active_firm=$FIRM_ID") \
    || fail "expected /financials?tab=aging to render the 0-30 bucket"
log "financials (UI path) confirmed"

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

# --- Rule Engine Builder UI backend: create a rule through the real API,
# confirm it fires on a transition (deferred from the last batch, which
# only wired the rule engine and unit/integration-tested it - this is the
# genuine end-to-end proof that was flagged as missing) --------------------

log "rule engine: creating a notify-on-record_sale rule for stock_to_sale"
RULE_CREATE_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$FIRM_ID/workflow-definitions/stock_to_sale/rules" \
    -H "Content-Type: application/json" \
    -d '{
      "name": "E2E notify on sale",
      "trigger": "on_transition",
      "conditionTree": {"type":"field","field":"quantity","op":"gt","value":0},
      "actions": [{"type":"notify","channel":"audit_log","messageTemplate":"e2e sold {{quantity}} of {{item}}"}],
      "autonomous": true,
      "enabled": true
    }')
RULE_ID=$(echo "$RULE_CREATE_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['id'])")
[ -n "$RULE_ID" ] || fail "did not get an id back from creating the rule: $RULE_CREATE_RESPONSE"
log "rule created: $RULE_ID"

log "rule engine: confirming GET .../rules lists the new rule"
RULES_LIST=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/workflow-definitions/stock_to_sale/rules")
echo "$RULES_LIST" | python3 -c "
import sys, json
rules = json.load(sys.stdin)
matches = [r for r in rules if r['id'] == '$RULE_ID']
assert matches, rules
assert matches[0]['name'] == 'E2E notify on sale', matches[0]
" || fail "expected the new rule to be listed: $RULES_LIST"
log "rule listing confirmed"

log "rule engine: adding a second stock instance and selling it, to fire the new rule"
RULE_INSTANCE_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$FIRM_ID/workflow-definitions/$DEFINITION_ID/instances" \
    -H "Content-Type: application/json" \
    -d '{"payload":{"item":"E2E Rule Widget","quantity":7}}')
RULE_INSTANCE_ID=$(echo "$RULE_INSTANCE_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['instanceId'])")
[ -n "$RULE_INSTANCE_ID" ] || fail "did not get an instanceId back from add-stock (rule test): $RULE_INSTANCE_RESPONSE"

auth -X POST "$BACKEND_URL/api/firms/$FIRM_ID/workflow-instances/$RULE_INSTANCE_ID/transitions/record_sale" \
    -H "Content-Type: application/json" -d '{}' > /dev/null

log "rule engine: confirming the rule's notify action actually executed (a real audit_log row, not a mock)"
RULE_AUDIT_LOG=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/audit-log")
echo "$RULE_AUDIT_LOG" | python3 -c "
import sys, json
entries = json.load(sys.stdin)
matches = [
    e for e in entries
    if e['entityType'] == 'workflow_instance' and e['action'] == 'notify'
    and e['changes'].get('ruleId') == '$RULE_ID'
]
assert matches, entries
assert matches[0]['changes'].get('message') == 'e2e sold 7 of E2E Rule Widget', matches[0]
" || fail "expected the rule's notify action to have written a matching audit_log entry: $RULE_AUDIT_LOG"
log "rule engine: rule fired on transition, confirmed via a real audit_log entry"

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

log "wizard content (Open Points item 12/37): confirming FIRM_ID's wizard answers (sells+inventory+CRM=yes, purchase+tasks=no) seeded EXACTLY stock_to_sale and customer_pipeline - not purchase_order/task_approval, and not the old hardcoded pair regardless of answers"
echo "$DEFINITIONS_LIST" | python3 -c "
import sys, json
defs = json.load(sys.stdin)
keys = {d['key'] for d in defs}
assert keys == {'stock_to_sale', 'customer_pipeline'}, keys
" || fail "expected FIRM_ID to have exactly {stock_to_sale, customer_pipeline} seeded, got: $DEFINITIONS_LIST"
log "wizard content seeding confirmed"

SECOND_FIRM_NAME="E2E Smoke Firm 2 $(date +%s)"
log "wizard: creating a second firm for the same user ('$SECOND_FIRM_NAME'), to exercise the firm switcher"
# This firm only needs to exist for the firm-switch check below - answers
# "no" all the way through, so it's created with zero seeded workflows
# (Open Points item 37).
SECOND_ANSWER_RESPONSE=$(wizard_walk "$SECOND_FIRM_NAME" no no no no no)
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
WORKFLOWS_LIST_HTML=$(fetchPageUntilContains "$FRONTEND_URL/en/workflows" "Purchase Order" -b "zonaryos_session=$TOKEN; zonaryos_active_firm=$FIRM_ID") \
    || fail "expected the frontend's /workflows list page to render the newly-defined Purchase Order definition"
log "workflows list (UI path) confirmed"

log "generic workflow view (UI path): confirming /workflows/purchase_order renders through the SAME generic component tree as /stock, with zero purchase_order-specific frontend code"
PO_VIEW_HTML=$(fetchPageUntilContains "$FRONTEND_URL/en/workflows/purchase_order" "Acme Supply Co" -b "zonaryos_session=$TOKEN; zonaryos_active_firm=$FIRM_ID") \
    || fail "expected /workflows/purchase_order to render the new instance's payload (vendor: Acme Supply Co) via the generic formatPayload rendering"
grep -q "Purchase Order" <<<"$PO_VIEW_HTML" \
    || fail "expected /workflows/purchase_order to render the definition's own name"
grep -q "Approve" <<<"$PO_VIEW_HTML" \
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

# --- Logistics + customer-facing data: the CustomerTemplate on
# --- customer_pipeline's own "convert" transition (there is no
# --- "qualify_to_customer" transition in the real spec - the qualified ->
# --- customer transition has always been named "convert") should have
# --- auto-created a real customers row from the lead's own payload
# --- (name="E2E Smoke Lead", carried on the merged instance payload since
# --- creation). Then a fresh stock_to_sale sale referencing that customer
# --- via customer_id proves the workflow -> ledger bridge's description
# --- interpolation picks up the customer's name, and a delivery address on
# --- the same sale proves the DeliveryTemplate side of the same
# --- transition fires in the same transaction. -------------------------

log "customers: confirming the convert transition auto-created a real customer record"
CUSTOMERS_RESPONSE=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/customers")
CUSTOMER_ID=$(echo "$CUSTOMERS_RESPONSE" | python3 -c "
import sys, json
customers = json.load(sys.stdin)
matches = [c for c in customers if c.get('sourceWorkflowInstance') == '$LEAD_ID']
assert len(matches) == 1, customers
assert matches[0]['name'] == 'E2E Smoke Lead', matches[0]
print(matches[0]['id'])
")
[ -n "$CUSTOMER_ID" ] || fail "expected a customer auto-created from the converted lead: $CUSTOMERS_RESPONSE"
log "customer auto-created: $CUSTOMER_ID (source_workflow_instance=$LEAD_ID)"

log "customers (UI path): confirming /customers renders the new customer"
CUSTOMERS_PAGE_HTML=$(fetchPageUntilContains "$FRONTEND_URL/en/customers" "E2E Smoke Lead" -b "zonaryos_session=$TOKEN; zonaryos_active_firm=$FIRM_ID") \
    || fail "expected /customers to render the converted lead's name (E2E Smoke Lead)"
log "customers (UI path) confirmed"

log "logistics: creating a second stock_to_sale instance referencing the new customer, with a delivery address"
CUSTOMER_SALE_INSTANCE=$(auth -X POST "$BACKEND_URL/api/firms/$FIRM_ID/workflow-definitions/$DEFINITION_ID/instances" \
    -H "Content-Type: application/json" \
    -d "{\"payload\":{\"item\":\"E2E Customer Widget\",\"quantity\":1,\"customer_id\":\"$CUSTOMER_ID\",\"destination_address\":\"789 Delivery Ln\",\"carrier\":\"E2E Carrier\"}}")
CUSTOMER_SALE_INSTANCE_ID=$(echo "$CUSTOMER_SALE_INSTANCE" | python3 -c "import sys, json; print(json.load(sys.stdin)['instanceId'])")
[ -n "$CUSTOMER_SALE_INSTANCE_ID" ] || fail "did not get an instanceId back from the customer-linked sale instance: $CUSTOMER_SALE_INSTANCE"
log "instance created: $CUSTOMER_SALE_INSTANCE_ID"

log "logistics + CRM: executing record_sale (customer_id + destination_address present)"
CUSTOMER_SALE_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$FIRM_ID/workflow-instances/$CUSTOMER_SALE_INSTANCE_ID/transitions/record_sale" \
    -H "Content-Type: application/json" -d '{"payload":{"unit_price":9.50}}')
echo "$CUSTOMER_SALE_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['state']['key'] == 'sold', d" \
    || fail "expected the instance's state to be sold after record_sale: $CUSTOMER_SALE_RESPONSE"
log "sale recorded: instance $CUSTOMER_SALE_INSTANCE_ID is now sold"

log "financial management core: confirming the journal entry's description includes the customer's name"
CUSTOMER_SALE_JOURNAL=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/journal-entries")
echo "$CUSTOMER_SALE_JOURNAL" | python3 -c "
import sys, json
entries = json.load(sys.stdin)
matches = [e for e in entries if e.get('sourceType') == 'workflow_instance' and e.get('sourceId') == '$CUSTOMER_SALE_INSTANCE_ID']
assert len(matches) == 1, entries
entry = matches[0]
assert entry['description'] == 'Sale of E2E Customer Widget (customer: E2E Smoke Lead)', entry
" || fail "expected the journal entry's description to name the customer: $CUSTOMER_SALE_JOURNAL"
log "journal entry description confirmed to include the customer's name"

log "logistics: confirming the DeliveryTemplate on the same transition auto-created a delivery"
DELIVERIES_RESPONSE=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/deliveries")
echo "$DELIVERIES_RESPONSE" | python3 -c "
import sys, json
deliveries = json.load(sys.stdin)
matches = [d for d in deliveries if d.get('sourceId') == '$CUSTOMER_SALE_INSTANCE_ID']
assert len(matches) == 1, deliveries
d = matches[0]
assert d['destinationAddress'] == '789 Delivery Ln', d
assert d['carrier'] == 'E2E Carrier', d
assert d['status'] == 'pending', d
assert d['sourceType'] == 'workflow_instance', d
" || fail "expected a delivery auto-created for the customer-linked sale: $DELIVERIES_RESPONSE"
log "delivery auto-created from the same transition, confirmed"

log "logistics (UI path): confirming /logistics renders the new delivery"
LOGISTICS_PAGE_HTML=$(fetchPageUntilContains "$FRONTEND_URL/en/logistics" "789 Delivery Ln" -b "zonaryos_session=$TOKEN; zonaryos_active_firm=$FIRM_ID") \
    || fail "expected /logistics to render the new delivery's destination address (789 Delivery Ln)"
log "logistics (UI path) confirmed"

log "reports: confirming activeCustomers and pendingDeliveries KPIs reflect the new records"
CRM_LOGISTICS_KPIS=$(auth "$BACKEND_URL/api/firms/$FIRM_ID/reports/kpis")
echo "$CRM_LOGISTICS_KPIS" | python3 -c "
import sys, json
kpis = {k['key']: k for k in json.load(sys.stdin)}
assert int(kpis['activeCustomers']['value']) >= 1, kpis['activeCustomers']
assert int(kpis['pendingDeliveries']['value']) >= 1, kpis['pendingDeliveries']
" || fail "expected activeCustomers>=1 and pendingDeliveries>=1: $CRM_LOGISTICS_KPIS"
log "reports: CRM/logistics KPIs confirmed"

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
DASHBOARD_HTML=$(fetchPageUntilContains "$FRONTEND_URL/en" "Customer Pipeline" -b "zonaryos_session=$TOKEN; zonaryos_active_firm=$FIRM_ID") \
    || fail "expected the dashboard to render a Customer Pipeline overview card"
grep -q "Stock In -> Sale\|Stock In -&gt; Sale" <<<"$DASHBOARD_HTML" \
    || fail "expected the dashboard to render a Stock In -> Sale overview card"
log "dashboard overview (UI path) confirmed"

log "quick create (UI path): creating a second lead directly through the dashboard's generic create route, same as any other workflow"
QUICK_CREATE_RESPONSE=$(uiAuth -X POST "$FRONTEND_URL/api/workflow/instances" \
    -H "Content-Type: application/json" \
    -d "{\"firmId\":\"$FIRM_ID\",\"definitionId\":\"$CRM_DEFINITION_ID\",\"payload\":{\"name\":\"E2E Quick-Create Lead\"}}")
echo "$QUICK_CREATE_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['state']['key'] == 'lead', d" \
    || fail "expected the quick-created lead's state to be lead: $QUICK_CREATE_RESPONSE"
log "quick create (UI path) confirmed"

log "global search (UI path): confirming /search finds the converted lead, grouped under Customer Pipeline"
SEARCH_HTML=$(fetchPageUntilContains "$FRONTEND_URL/en/search?q=E2E%20Smoke%20Lead" "E2E Smoke Lead" -b "zonaryos_session=$TOKEN; zonaryos_active_firm=$FIRM_ID") \
    || fail "expected /search?q=E2E Smoke Lead to render the matching lead's payload"
grep -q "Customer Pipeline" <<<"$SEARCH_HTML" \
    || fail "expected /search?q=E2E Smoke Lead to render a Customer Pipeline results group"
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

# --- Financial management core: the Purchase Order workflow's own
# --- workflow-to-ledger bridge (internal/workflow/purchase_order.go) -----
#
# A separate firm, with purchases=yes (unlike FIRM_ID above, which
# answered purchase=no), so the real, Go-defined PurchaseOrderSpec is
# actually seeded - the earlier "define workflow (UI path)" section
# defines its own DIFFERENT, unrelated "purchase_order"-keyed spec (draft
# -> approved -> received, no journal template) on FIRM_ID purely to
# prove the generic UI renders a second, structurally different
# definition; it is not the workflow this section exercises.

log "purchase order: walking sells=no/purchase=yes/tasks=no/people=no/manufacture=no to create a firm that seeds the real Purchase Order workflow"
PO_ANSWER_RESPONSE=$(wizard_walk "E2E PO Firm $(date +%s)" no yes no no no)
PO_FIRM_ID=$(echo "$PO_ANSWER_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['result']['firmId'])")
[ -n "$PO_FIRM_ID" ] || fail "did not get a firmId back from the wizard answer: $PO_ANSWER_RESPONSE"
log "purchase order firm created: $PO_FIRM_ID"

log "purchase order: confirming the wizard seeded Inventory (1200) and Trade Payables (2000) even though this firm answered sells=no"
PO_FIRM_ACCOUNTS=$(auth "$BACKEND_URL/api/firms/$PO_FIRM_ID/accounts")
echo "$PO_FIRM_ACCOUNTS" | python3 -c "
import sys, json
accounts = {a['code'] for a in json.load(sys.stdin)}
assert '1200' in accounts, accounts
assert '2000' in accounts, accounts
" || fail "expected Inventory (1200) and Trade Payables (2000) to be seeded for a purchases-only firm: $PO_FIRM_ACCOUNTS"
log "purchase order: chart of accounts confirmed (Inventory/Trade Payables present without sells=yes)"

log "resolving the purchase_order workflow definition"
PO_DEFINITION_RESPONSE=$(auth "$BACKEND_URL/api/firms/$PO_FIRM_ID/workflow-definitions?key=purchase_order")
PO_REAL_DEFINITION_ID=$(echo "$PO_DEFINITION_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['definitionId'])")
[ -n "$PO_REAL_DEFINITION_ID" ] || fail "did not resolve the purchase_order definition: $PO_DEFINITION_RESPONSE"

log "purchase order: creating a draft purchase order instance"
PO_REAL_INSTANCE_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$PO_FIRM_ID/workflow-definitions/$PO_REAL_DEFINITION_ID/instances" \
    -H "Content-Type: application/json" \
    -d '{"payload":{"supplier_name":"Acme Supply Co","total_amount":349.50}}')
PO_REAL_INSTANCE_ID=$(echo "$PO_REAL_INSTANCE_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['instanceId'])")
[ -n "$PO_REAL_INSTANCE_ID" ] || fail "did not get an instanceId back: $PO_REAL_INSTANCE_RESPONSE"
echo "$PO_REAL_INSTANCE_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['state']['key'] == 'draft', d" \
    || fail "expected the new purchase order instance's state to be draft: $PO_REAL_INSTANCE_RESPONSE"
log "purchase order created: instance $PO_REAL_INSTANCE_ID, state draft"

log "purchase order: executing the send transition (must NOT post a journal entry - see PurchaseOrderSpec's own accounting reasoning)"
auth -X POST "$BACKEND_URL/api/firms/$PO_FIRM_ID/workflow-instances/$PO_REAL_INSTANCE_ID/transitions/send" \
    -H "Content-Type: application/json" -d '{}' > /dev/null

PO_ENTRIES_AFTER_SEND=$(auth "$BACKEND_URL/api/firms/$PO_FIRM_ID/journal-entries")
echo "$PO_ENTRIES_AFTER_SEND" | python3 -c "
import sys, json
entries = json.load(sys.stdin)
assert len(entries) == 0, entries
" || fail "expected 'send' to post no journal entry: $PO_ENTRIES_AFTER_SEND"
log "purchase order: confirmed 'send' posted nothing"

log "purchase order: executing the receive transition (must post DR Inventory / CR Trade Payables)"
PO_RECEIVE_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$PO_FIRM_ID/workflow-instances/$PO_REAL_INSTANCE_ID/transitions/receive" \
    -H "Content-Type: application/json" -d '{}')
echo "$PO_RECEIVE_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['state']['key'] == 'received', d" \
    || fail "expected the instance's state to be received: $PO_RECEIVE_RESPONSE"

PO_ENTRIES_AFTER_RECEIVE=$(auth "$BACKEND_URL/api/firms/$PO_FIRM_ID/journal-entries")
echo "$PO_ENTRIES_AFTER_RECEIVE" | python3 -c "
import sys, json
entries = json.load(sys.stdin)
matches = [e for e in entries if e.get('sourceType') == 'workflow_instance' and e.get('sourceId') == '$PO_REAL_INSTANCE_ID']
assert len(matches) == 1, entries
entry = matches[0]
assert entry['description'] == 'Received purchase order from Acme Supply Co', entry
lines = {(l['accountCode'], l['side']): l['amount'] for l in entry['lines']}
assert len(lines) == 2, entry
assert lines[('1200', 'debit')] == '349.5000', entry
assert lines[('2000', 'credit')] == '349.5000', entry
" || fail "expected a balanced journal entry (DR 1200 / CR 2000, 349.5000) posted for instance $PO_REAL_INSTANCE_ID: $PO_ENTRIES_AFTER_RECEIVE"
log "purchase order: journal entry confirmed on receive - DR Inventory / CR Trade Payables, 349.5000"

log "purchase order: confirming the Balance Sheet still balances after this real purchase"
PO_BALANCE_SHEET=$(auth "$BACKEND_URL/api/firms/$PO_FIRM_ID/reports/balance-sheet")
echo "$PO_BALANCE_SHEET" | python3 -c "
import sys, json
r = json.load(sys.stdin)
assert r['balanced'] is True, r
" || fail "expected the purchase order firm's balance sheet to balance: $PO_BALANCE_SHEET"
log "purchase order: balance sheet confirmed balanced"

# --- HR core + task/project management + reporting foundation: create a
# --- person, assign a real task to them, confirm both /tasks (the cross-
# --- module join) and /reports (the KPI dashboard) reflect it -----------

log "HR: creating a person"
PERSON_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$PO_FIRM_ID/people" \
    -H "Content-Type: application/json" \
    -d '{"fullName":"E2E Assignee","type":"employee","email":"assignee@example.com"}')
PERSON_ID=$(echo "$PERSON_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['id'])")
[ -n "$PERSON_ID" ] || fail "did not get an id back from creating a person: $PERSON_RESPONSE"
log "person created: $PERSON_ID"

log "HR: confirming GET .../people lists the new person"
PEOPLE_LIST=$(auth "$BACKEND_URL/api/firms/$PO_FIRM_ID/people")
echo "$PEOPLE_LIST" | python3 -c "
import sys, json
people = json.load(sys.stdin)
matches = [p for p in people if p['id'] == '$PERSON_ID']
assert matches, people
assert matches[0]['fullName'] == 'E2E Assignee', matches[0]
" || fail "expected the new person to be listed: $PEOPLE_LIST"
log "HR: person listing confirmed"

log "HR: adding a contract for the person"
CONTRACT_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$PO_FIRM_ID/people/$PERSON_ID/contracts" \
    -H "Content-Type: application/json" \
    -d '{"type":"salary","amount":"20000.00","startDate":"2024-01-01"}')
CONTRACT_ID=$(echo "$CONTRACT_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['id'])")
[ -n "$CONTRACT_ID" ] || fail "did not get an id back from creating a contract: $CONTRACT_RESPONSE"
log "contract created: $CONTRACT_ID"

log "tasks: resolving the task_approval workflow definition (this firm answered tasks=no in its wizard walk, seeding it directly via DefineWorkflowForFirm instead)"
TASK_DEFINE_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$PO_FIRM_ID/workflow-definitions" \
    -H "Content-Type: application/json" \
    -d '{
      "key":"task_approval",
      "name":"Task / Approval",
      "createPermission":{"key":"workflow.task_approval.create","description":"Create a task."},
      "states":[
        {"key":"open","name":"Open","isInitial":true},
        {"key":"in_progress","name":"In Progress"},
        {"key":"done","name":"Done","isTerminal":true},
        {"key":"rejected","name":"Rejected","isTerminal":true}
      ],
      "transitions":[
        {"fromStateKey":"open","toStateKey":"in_progress","actionKey":"start","name":"Start","permission":{"key":"workflow.task_approval.start","description":"Start a task."}},
        {"fromStateKey":"in_progress","toStateKey":"done","actionKey":"complete","name":"Complete","permission":{"key":"workflow.task_approval.complete","description":"Complete a task."}}
      ],
      "fields":[{"name":"assignee_person_id","type":"person","required":false}]
    }')
TASK_DEFINITION_ID=$(echo "$TASK_DEFINE_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['definitionId'])")
[ -n "$TASK_DEFINITION_ID" ] || fail "did not get a definitionId back from defining task_approval: $TASK_DEFINE_RESPONSE"
log "task_approval workflow defined: $TASK_DEFINITION_ID"

log "tasks: creating a task assigned to the new person"
TASK_INSTANCE_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$PO_FIRM_ID/workflow-definitions/$TASK_DEFINITION_ID/instances" \
    -H "Content-Type: application/json" \
    -d "{\"payload\":{\"assignee_person_id\":\"$PERSON_ID\"}}")
TASK_INSTANCE_ID=$(echo "$TASK_INSTANCE_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['instanceId'])")
[ -n "$TASK_INSTANCE_ID" ] || fail "did not get an instanceId back from creating a task: $TASK_INSTANCE_RESPONSE"
echo "$TASK_INSTANCE_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['payload']['assignee_person_id'] == '$PERSON_ID', d" \
    || fail "expected the task's assignee_person_id to round-trip: $TASK_INSTANCE_RESPONSE"
log "task created and assigned: instance $TASK_INSTANCE_ID"

log "tasks: confirming a task assigned to a NONEXISTENT person is rejected"
BAD_ASSIGNEE_STATUS=$(auth -o /dev/null -w '%{http_code}' -X POST "$BACKEND_URL/api/firms/$PO_FIRM_ID/workflow-definitions/$TASK_DEFINITION_ID/instances" \
    -H "Content-Type: application/json" \
    -d '{"payload":{"assignee_person_id":"00000000-0000-0000-0000-000000000000"}}')
[ "$BAD_ASSIGNEE_STATUS" = "400" ] || fail "expected 400 for a nonexistent assignee, got $BAD_ASSIGNEE_STATUS"
log "tasks: nonexistent-assignee rejection confirmed"

# By this point in the script the user owns several firms (FIRM_ID,
# SECOND_FIRM_ID, PO_FIRM_ID, ...) - without an explicit active-firm
# cookie, src/lib/activeFirm.ts's resolveActiveFirm falls back to
# me.firms[0] (the FIRST-created firm), not PO_FIRM_ID, which is where
# this section's data actually lives. Setting zonaryos_active_firm
# directly (the same cookie the real firm-switcher UI sets, see
# app/api/firm/switch/route.ts) is the honest way to address it - no
# different from a real user having switched firms in their browser.
log "tasks (UI path): confirming /tasks renders the assignee's name (the cross-module join)"
TASKS_PAGE_HTML=$(fetchPageUntilContains "$FRONTEND_URL/en/tasks" "E2E Assignee" -b "zonaryos_session=$TOKEN; zonaryos_active_firm=$PO_FIRM_ID") \
    || fail "expected /tasks to render the assignee's name (E2E Assignee) via the workflow-instances + people join"
log "tasks (UI path) confirmed - cross-module join renders correctly"

log "reports: confirming GET .../reports/kpis reflects the real open task and active person just created"
KPIS_RESPONSE=$(auth "$BACKEND_URL/api/firms/$PO_FIRM_ID/reports/kpis")
echo "$KPIS_RESPONSE" | python3 -c "
import sys, json
kpis = {k['key']: k for k in json.load(sys.stdin)}
assert kpis['openTasks']['value'] == '1', kpis['openTasks']
assert kpis['activePeople']['value'] == '1', kpis['activePeople']
" || fail "expected openTasks=1 and activePeople=1 in the KPI dashboard: $KPIS_RESPONSE"
log "reports: KPI dashboard confirmed (openTasks=1, activePeople=1)"

log "reports (UI path): confirming /reports renders the KPI dashboard"
REPORTS_PAGE_STATUS=$(curl -sS -o /dev/null -w '%{http_code}' -b "zonaryos_session=$TOKEN; zonaryos_active_firm=$PO_FIRM_ID" "$FRONTEND_URL/en/reports")
[ "$REPORTS_PAGE_STATUS" = "200" ] || fail "expected /reports to render (200), got $REPORTS_PAGE_STATUS"
log "reports (UI path) confirmed"

log "HR (UI path): confirming /hr renders the new person"
HR_PAGE_HTML=$(fetchPageUntilContains "$FRONTEND_URL/en/hr" "E2E Assignee" -b "zonaryos_session=$TOKEN; zonaryos_active_firm=$PO_FIRM_ID") \
    || fail "expected /hr to render the new person's name (E2E Assignee)"
log "HR (UI path) confirmed"

# --- Inventory management, warehouse, and supplier management: a
# --- dedicated firm answering yes to BOTH sells+tracks-inventory (so
# --- SeedStockToSaleWorkflowTx seeds the real stock_to_sale spec) AND
# --- purchases-from-suppliers (so SeedPurchaseOrderWorkflowTx seeds the
# --- real purchase_order spec, internal/wizard/firm.go's own two seeding
# --- conditions) - neither FIRM_ID (purchases=no) nor PO_FIRM_ID
# --- (sells=no) has both, and FIRM_ID's own "purchase_order" key is a
# --- DIFFERENT, UI-defined custom spec (see the comment above PO_FIRM_ID's
# --- own section) with none of this batch's product_id/supplier_id/
# --- quantity fields - create a product, receive it via the real
# --- purchase_order's "purchase_receive" hook (the only HTTP-reachable way
# --- to put real stock on hand), sell it via the real stock_to_sale's
# --- own hook, confirm stock dropped + a movement row exists, and confirm
# --- /inventory and /suppliers render it -------------------------------

log "inventory: creating a firm that both sells (tracks inventory) and purchases from suppliers"
INV_ANSWER_RESPONSE=$(wizard_walk "E2E Inventory Firm $(date +%s)" yes yes no yes no no no)
INV_FIRM_ID=$(echo "$INV_ANSWER_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['result']['firmId'])")
[ -n "$INV_FIRM_ID" ] || fail "did not get a firmId back from the wizard answer: $INV_ANSWER_RESPONSE"
log "inventory firm created: $INV_FIRM_ID"

INV_STOCK_DEFINE=$(auth "$BACKEND_URL/api/firms/$INV_FIRM_ID/workflow-definitions?key=stock_to_sale")
INV_STOCK_DEFINITION_ID=$(echo "$INV_STOCK_DEFINE" | python3 -c "import sys, json; print(json.load(sys.stdin)['definitionId'])")
[ -n "$INV_STOCK_DEFINITION_ID" ] || fail "did not resolve the inventory firm's stock_to_sale definition: $INV_STOCK_DEFINE"
INV_PO_DEFINE=$(auth "$BACKEND_URL/api/firms/$INV_FIRM_ID/workflow-definitions?key=purchase_order")
INV_PO_DEFINITION_ID=$(echo "$INV_PO_DEFINE" | python3 -c "import sys, json; print(json.load(sys.stdin)['definitionId'])")
[ -n "$INV_PO_DEFINITION_ID" ] || fail "did not resolve the inventory firm's purchase_order definition: $INV_PO_DEFINE"

log "inventory: creating a product"
PRODUCT_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$INV_FIRM_ID/products" \
    -H "Content-Type: application/json" \
    -d '{"sku":"E2E-WIDGET","name":"E2E Stock Widget","costPrice":"4.00","minQuantity":"5"}')
PRODUCT_ID=$(echo "$PRODUCT_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['id'])")
[ -n "$PRODUCT_ID" ] || fail "did not get an id back from creating a product: $PRODUCT_RESPONSE"
log "product created: $PRODUCT_ID"

log "inventory: creating a stock_to_sale instance referencing the product"
STOCK_INSTANCE_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$INV_FIRM_ID/workflow-definitions/$INV_STOCK_DEFINITION_ID/instances" \
    -H "Content-Type: application/json" \
    -d "{\"payload\":{\"item\":\"E2E Stock Widget\",\"product_id\":\"$PRODUCT_ID\"}}")
STOCK_INSTANCE_ID=$(echo "$STOCK_INSTANCE_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['instanceId'])")
[ -n "$STOCK_INSTANCE_ID" ] || fail "did not get an instanceId back: $STOCK_INSTANCE_RESPONSE"
log "instance created: $STOCK_INSTANCE_ID"

log "inventory: confirming a sale with no on-hand stock is rejected (400, ErrInsufficientStock)"
INSUFFICIENT_STOCK_STATUS=$(auth -o /dev/null -w '%{http_code}' -X POST "$BACKEND_URL/api/firms/$INV_FIRM_ID/workflow-instances/$STOCK_INSTANCE_ID/transitions/record_sale" \
    -H "Content-Type: application/json" -d '{"payload":{"quantity":1}}')
[ "$INSUFFICIENT_STOCK_STATUS" = "400" ] || fail "expected 400 for a sale against zero stock, got $INSUFFICIENT_STOCK_STATUS"
log "inventory: insufficient-stock rejection confirmed"

log "inventory: topping up stock via a purchase_order instance (purchase_receive hook)"
PO_STOCK_TOPUP_INSTANCE=$(auth -X POST "$BACKEND_URL/api/firms/$INV_FIRM_ID/workflow-definitions/$INV_PO_DEFINITION_ID/instances" \
    -H "Content-Type: application/json" \
    -d "{\"payload\":{\"supplier_name\":\"E2E Supply Co\",\"product_id\":\"$PRODUCT_ID\"}}")
PO_STOCK_TOPUP_INSTANCE_ID=$(echo "$PO_STOCK_TOPUP_INSTANCE" | python3 -c "import sys, json; print(json.load(sys.stdin)['instanceId'])")
[ -n "$PO_STOCK_TOPUP_INSTANCE_ID" ] || fail "did not get an instanceId back for the stock top-up purchase order: $PO_STOCK_TOPUP_INSTANCE"
auth -X POST "$BACKEND_URL/api/firms/$INV_FIRM_ID/workflow-instances/$PO_STOCK_TOPUP_INSTANCE_ID/transitions/send" >/dev/null
auth -X POST "$BACKEND_URL/api/firms/$INV_FIRM_ID/workflow-instances/$PO_STOCK_TOPUP_INSTANCE_ID/transitions/receive" \
    -H "Content-Type: application/json" -d '{"payload":{"quantity":10}}' >/dev/null
log "inventory: 10 units received via the 'purchase_receive' hook"

log "inventory: confirming GET .../products/{id}/stock shows 10 units on hand"
STOCK_AFTER_RECEIVE=$(auth "$BACKEND_URL/api/firms/$INV_FIRM_ID/products/$PRODUCT_ID/stock")
echo "$STOCK_AFTER_RECEIVE" | python3 -c "
import sys, json
levels = json.load(sys.stdin)
assert len(levels) == 1 and levels[0]['quantity'] == '10.0000', levels
" || fail "expected 10.0000 units on hand after the purchase receipt: $STOCK_AFTER_RECEIVE"
log "inventory: stock level confirmed at 10.0000"

log "inventory: selling 4 units through the stock_to_sale instance"
STOCK_SALE_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$INV_FIRM_ID/workflow-instances/$STOCK_INSTANCE_ID/transitions/record_sale" \
    -H "Content-Type: application/json" -d '{"payload":{"quantity":4}}')
echo "$STOCK_SALE_RESPONSE" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['state']['key'] == 'sold', d" \
    || fail "expected the instance's state to be sold after record_sale: $STOCK_SALE_RESPONSE"
log "inventory: sale recorded"

log "inventory: confirming stock dropped to 6.0000 and a 'sale' movement row exists"
STOCK_AFTER_SALE=$(auth "$BACKEND_URL/api/firms/$INV_FIRM_ID/products/$PRODUCT_ID/stock")
echo "$STOCK_AFTER_SALE" | python3 -c "
import sys, json
levels = json.load(sys.stdin)
assert len(levels) == 1 and levels[0]['quantity'] == '6.0000', levels
" || fail "expected 6.0000 units on hand after the sale: $STOCK_AFTER_SALE"

MOVEMENTS_RESPONSE=$(auth "$BACKEND_URL/api/firms/$INV_FIRM_ID/stock-movements?productId=$PRODUCT_ID")
echo "$MOVEMENTS_RESPONSE" | python3 -c "
import sys, json
d = json.load(sys.stdin)
movements = d['movements']
sale_moves = [m for m in movements if m['reason'] == 'sale' and m['sourceId'] == '$STOCK_INSTANCE_ID']
assert len(sale_moves) == 1, movements
assert sale_moves[0]['quantityChange'] == '-4.0000', sale_moves[0]
purchase_moves = [m for m in movements if m['reason'] == 'purchase']
assert len(purchase_moves) == 1, movements
" || fail "expected exactly one sale movement (-4.0000) and one purchase movement for this product: $MOVEMENTS_RESPONSE"
log "inventory: stock movement history confirmed (purchase +10, sale -4)"

log "inventory (UI path): confirming /inventory renders the product and its stock"
INVENTORY_PAGE_HTML=$(fetchPageUntilContains "$FRONTEND_URL/en/inventory" "E2E-WIDGET" -b "zonaryos_session=$TOKEN; zonaryos_active_firm=$INV_FIRM_ID") \
    || fail "expected /inventory to render the new product's SKU (E2E-WIDGET)"
log "inventory (UI path) confirmed"

log "suppliers: creating a supplier"
SUPPLIER_RESPONSE=$(auth -X POST "$BACKEND_URL/api/firms/$INV_FIRM_ID/suppliers" \
    -H "Content-Type: application/json" \
    -d '{"name":"E2E Supply Partners","email":"sales@e2e-supply.example"}')
SUPPLIER_ID=$(echo "$SUPPLIER_RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['id'])")
[ -n "$SUPPLIER_ID" ] || fail "did not get an id back from creating a supplier: $SUPPLIER_RESPONSE"
log "supplier created: $SUPPLIER_ID"

log "suppliers (UI path): confirming /suppliers renders the new supplier"
SUPPLIERS_PAGE_HTML=$(fetchPageUntilContains "$FRONTEND_URL/en/suppliers" "E2E Supply Partners" -b "zonaryos_session=$TOKEN; zonaryos_active_firm=$INV_FIRM_ID") \
    || fail "expected /suppliers to render the new supplier's name (E2E Supply Partners)"
log "suppliers (UI path) confirmed"

log "reports: confirming GET .../reports/kpis reflects the new low-stock-free product and its inventory value"
INVENTORY_KPIS_RESPONSE=$(auth "$BACKEND_URL/api/firms/$INV_FIRM_ID/reports/kpis")
echo "$INVENTORY_KPIS_RESPONSE" | python3 -c "
import sys, json
kpis = {k['key']: k for k in json.load(sys.stdin)}
# 6.0000 on hand * 4.00 cost = 24.0000 - this firm's only stocked product.
assert kpis['totalInventoryValue']['value'] == '24.0000', kpis['totalInventoryValue']
# 6.0000 on hand >= min_quantity 5 - NOT low stock.
assert kpis['lowStockProducts']['value'] == '0', kpis['lowStockProducts']
" || fail "expected totalInventoryValue=24.0000 and lowStockProducts=0: $INVENTORY_KPIS_RESPONSE"
log "reports: inventory KPIs confirmed (totalInventoryValue=24.0000, lowStockProducts=0)"

# --- Import/export engine (Vision §3, avoids vendor lock-in): export
# --- FIRM_ID's complete configuration and data, import ONLY the
# --- configuration portion into a brand-new firm, confirm the import is
# --- idempotent (same document imported twice yields Imported the first
# --- time and Skipped the second), and confirm the /settings UI itself
# --- exposes both actions -------------------------------------------------

log "portability: exporting FIRM_ID's complete configuration and data"
EXPORT_HEADERS=$(mktemp)
EXPORT_DOCUMENT=$(auth -D "$EXPORT_HEADERS" "$BACKEND_URL/api/firms/$FIRM_ID/export")
grep -qi '^content-disposition: *attachment; *filename="zonaryos-export.json"' "$EXPORT_HEADERS" \
    || fail "expected GET .../export to set a download Content-Disposition header: $(cat "$EXPORT_HEADERS")"
rm -f "$EXPORT_HEADERS"
echo "$EXPORT_DOCUMENT" | python3 -c "
import sys, json
d = json.load(sys.stdin)
assert d['version'] == '1', d['version']
assert d['firm']['Name'], d['firm']
assert any(a['Code'] == '1000' for a in d['accounts']), d['accounts']
assert any(wd['Key'] == 'stock_to_sale' for wd in d['workflow_definitions']), d['workflow_definitions']
assert any(wd['Key'] == 'customer_pipeline' for wd in d['workflow_definitions']), d['workflow_definitions']
assert len(d['rules']) >= 1, d['rules']
assert len(d['journal_entries']) >= 1, 'expected historical data (journal entries) in the export too'
" || fail "exported document missing expected structure: $EXPORT_DOCUMENT"
log "portability: export document structure confirmed (version, firm, accounts, workflow definitions, rules, journal entries)"

log "portability: creating a brand-new, empty firm as the import target"
IMPORT_TARGET_FIRM_NAME="E2E Import Target $(date +%s)"
IMPORT_TARGET_ANSWER=$(wizard_walk "$IMPORT_TARGET_FIRM_NAME" no no no no no)
IMPORT_TARGET_FIRM_ID=$(echo "$IMPORT_TARGET_ANSWER" | python3 -c "import sys, json; print(json.load(sys.stdin)['result']['firmId'])")
[ -n "$IMPORT_TARGET_FIRM_ID" ] || fail "did not get a firmId back for the import target firm: $IMPORT_TARGET_ANSWER"
log "portability: import target firm created: $IMPORT_TARGET_FIRM_ID"

log "portability: importing the exported configuration into the new firm (first import - everything should be new)"
IMPORT_RESPONSE_1=$(auth -X POST "$BACKEND_URL/api/firms/$IMPORT_TARGET_FIRM_ID/import/configuration" \
    -H "Content-Type: application/json" -d "$EXPORT_DOCUMENT")
echo "$IMPORT_RESPONSE_1" | python3 -c "
import sys, json
d = json.load(sys.stdin)
assert d['workflowDefinitions']['imported'] >= 2, d
assert d['workflowDefinitions']['skipped'] == 0, d
assert d['rules']['imported'] >= 1, d
assert d['accounts']['imported'] >= 1, d
" || fail "expected the first import to report everything Imported, nothing Skipped: $IMPORT_RESPONSE_1"
log "portability: first import confirmed (workflow definitions, rules, accounts all Imported)"

log "portability: confirming the imported workflow definition is actually usable in the new firm"
IMPORTED_DEFINITION=$(auth "$BACKEND_URL/api/firms/$IMPORT_TARGET_FIRM_ID/workflow-definitions?key=stock_to_sale")
echo "$IMPORTED_DEFINITION" | python3 -c "import sys, json; d = json.load(sys.stdin); assert d['key'] == 'stock_to_sale', d" \
    || fail "expected stock_to_sale to exist in the import target firm after import: $IMPORTED_DEFINITION"
log "portability: imported stock_to_sale definition confirmed readable in the new firm"

log "portability: importing the exact same document a second time (idempotency - everything should now be Skipped)"
IMPORT_RESPONSE_2=$(auth -X POST "$BACKEND_URL/api/firms/$IMPORT_TARGET_FIRM_ID/import/configuration" \
    -H "Content-Type: application/json" -d "$EXPORT_DOCUMENT")
IMPORT_RESPONSE_1_FILE=$(mktemp)
IMPORT_RESPONSE_2_FILE=$(mktemp)
echo "$IMPORT_RESPONSE_1" > "$IMPORT_RESPONSE_1_FILE"
echo "$IMPORT_RESPONSE_2" > "$IMPORT_RESPONSE_2_FILE"
python3 -c "
import json
with open('$IMPORT_RESPONSE_1_FILE') as f:
    first = json.load(f)
with open('$IMPORT_RESPONSE_2_FILE') as f:
    second = json.load(f)
for kind in ('workflowDefinitions', 'rules', 'accounts', 'products'):
    assert second[kind]['imported'] == 0, (kind, second)
    # Everything the document contains is Skipped the second time around -
    # both what round 1 newly Imported AND whatever round 1 itself already
    # found pre-existing (Skipped) in the fresh import target firm (e.g.
    # the wizard's own default chart-of-accounts seeding), since both are
    # still present in round 2.
    assert second[kind]['skipped'] == first[kind]['imported'] + first[kind]['skipped'], (kind, first, second)
" || fail "expected the second import to Skip everything the first import Imported: first=$IMPORT_RESPONSE_1 second=$IMPORT_RESPONSE_2"
rm -f "$IMPORT_RESPONSE_1_FILE" "$IMPORT_RESPONSE_2_FILE"
log "portability: import idempotency confirmed (second import Skipped everything, nothing duplicated)"

log "settings: confirming the /settings page renders both the Export button and the Import configuration section"
SETTINGS_PAGE_HTML=$(curl -sS -b "zonaryos_session=$TOKEN; zonaryos_active_firm=$FIRM_ID" "$FRONTEND_URL/en/settings")
grep -qi "Export" <<<"$SETTINGS_PAGE_HTML" \
    || fail "expected /settings to render an Export button: (page HTML omitted)"
grep -qi "Import configuration" <<<"$SETTINGS_PAGE_HTML" \
    || fail "expected /settings to render an Import configuration section: (page HTML omitted)"
log "settings: Export/Import UI confirmed"

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
echo "E2E SMOKE TEST PASSED: login + wizard -> firm creation -> add stock -> sell, plus UI-path add stock, firm switch, audit log view, a real second workflow defined + rendered through the generic UI, the Customer Pipeline default workflow walked lead -> qualified -> customer, the convert transition's CustomerTemplate auto-creating a real customer record confirmed via /customers, a customer-linked sale whose DeliveryTemplate auto-created a delivery confirmed via /logistics and whose journal entry description names the customer, the activeCustomers/pendingDeliveries KPIs, (new) Invoicing/payment tracking - the same sale's InvoiceTemplate auto-creating a draft invoice with a matching line confirmed via /invoices, sent then paid via a recorded payment that auto-closed it and posted a real DR Cash / CR Trade Receivables journal entry, and the receivables aging report confirmed both via the API and /financials?tab=aging, the dashboard's counts-by-state overview, quick-create, global search, a full self-registration -> invite -> accept flow bringing a second real user into the firm with no email infrastructure, a purchase order walked draft -> sent -> received posting the correct ledger entry only on receive, (new) HR core (person + contract creation), a task assigned to that person confirmed via /tasks' cross-module join and the /reports KPI dashboard, (new) Inventory management - a product created, stock topped up via the purchase_order 'purchase_receive' hook, an insufficient-stock sale rejected then a real sale decreasing stock and appending a movement row, confirmed via /inventory and /suppliers and the inventory KPI dashboard, (new) the import/export engine - a full-firm export document's structure confirmed, its configuration imported into a brand-new firm (workflow definitions/rules/accounts all Imported), the imported workflow definition confirmed usable, the exact same document imported a second time confirmed fully idempotent (everything Skipped, nothing duplicated), and the /settings page confirmed to render both the Export button and the Import configuration section - and a genuine session-refresh proof - a real Keycloak access token actually expires, gets silently refreshed by src/proxy.ts with no re-login prompt, and the terminal (invalid refresh token) case cleanly falls through to a real login redirect - all against a real stack"
