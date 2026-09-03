#!/usr/bin/env bash
# Local, offline unit tests for .github/agent/lib.sh - the checklist/state
# parsing that .github/agent/loop.sh relies on. No network, no gh, no git;
# every test runs against a fixture body string, so this can (and should)
# be run and re-run without burning an Actions minute or a Gemini request.
#
# Usage: .github/agent/tests/run_tests.sh

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FIXTURES="$HERE/fixtures"
# shellcheck source=../lib.sh
source "$HERE/../lib.sh"

pass=0
fail=0

assert_eq() {
  local desc="$1" expected="$2" actual="$3"
  if [ "$expected" == "$actual" ]; then
    pass=$((pass + 1))
    echo "ok   - $desc"
  else
    fail=$((fail + 1))
    echo "FAIL - $desc"
    echo "       expected: $(printf '%q' "$expected")"
    echo "       actual:   $(printf '%q' "$actual")"
  fi
}

assert_status() {
  local desc="$1" expected_status="$2"
  shift 2
  local actual_status=0
  "$@" >/dev/null 2>&1 || actual_status=$?
  assert_eq "$desc" "$expected_status" "$actual_status"
}

f() { cat "$FIXTURES/$1"; }

# --- checklist parsing -----------------------------------------------------

assert_eq "checklist_total counts all 5 items on a fresh issue" \
  5 "$(checklist_total "$(f fresh.md)")"

assert_eq "checklist_total ignores checkboxes outside the Checklist section" \
  2 "$(checklist_total "$(f unrelated_checkbox.md)")"

assert_eq "checklist_total rejects a 61-item checklist over the 60 cap (cap enforced in loop.sh)" \
  61 "$(checklist_total "$(f large.md)")"

assert_eq "first_unchecked_index finds item 1 on a fresh issue" \
  1 "$(first_unchecked_index "$(f fresh.md)")"

assert_eq "first_unchecked_index skips the checked item 1 on midway.md and finds item 2" \
  2 "$(first_unchecked_index "$(f midway.md)")"

assert_status "first_unchecked_index fails (no unchecked items) on finished.md" \
  1 first_unchecked_index "$(f finished.md)"

assert_eq "first_unchecked_index ignores the unrelated decoy checkbox and still finds item 1" \
  1 "$(first_unchecked_index "$(f unrelated_checkbox.md)")"

assert_eq "item_text_at strips the '- [ ] ' prefix, not just any leading text" \
  "Add a Go doc comment to DeliveryEffect in internal/workflow/spec.go, following the existing doc-comment style in that file. Change nothing else." \
  "$(item_text_at "$(f midway.md)" 2)"

assert_eq "item_text_at works on an already-checked item too ('- [x] ' is also 6 chars)" \
  "Add a Go doc comment to StockEffect in internal/workflow/spec.go, following the existing doc-comment style in that file. Change nothing else." \
  "$(item_text_at "$(f midway.md)" 1)"

# --- tick_item: exactly one box flips, nothing else changes ---------------

ticked="$(tick_item "$(f fresh.md)" 1)"
assert_eq "tick_item flips item 1 to checked" \
  1 "$(grep -c '^- \[x\] Add a Go doc comment to StockEffect' <<<"$ticked")"
assert_eq "tick_item leaves items 2-5 unchecked" \
  4 "$(grep -c '^- \[ \] ' <<<"$ticked")"
assert_eq "tick_item leaves the total item count unchanged" \
  5 "$(checklist_total "$ticked")"
assert_eq "tick_item changes exactly one line: everything else is byte-for-byte identical to the input" \
  "$(f fresh.md | sed '2s/^- \[ \]/- [x]/')" "$ticked"

ticked2="$(tick_item "$(f midway.md)" 2)"
assert_eq "tick_item on midway.md flips item 2, keeps item 1's existing check" \
  2 "$(grep -c '^- \[x\] ' <<<"$ticked2")"
decoy_ticked="$(tick_item "$(f unrelated_checkbox.md)" 1)"
assert_eq "tick_item on unrelated_checkbox.md only flips checklist item 1, decoys stay as they were" \
  "- [ ] Not a checklist item - this is a decoy checkbox in an unrelated section and must not be counted or touched." \
  "$(grep '^- \[ \] Not a checklist item' <<<"$decoy_ticked")"
assert_eq "... and the already-checked decoy is still checked, not re-toggled" \
  "- [x] Another decoy, already checked." \
  "$(grep '^- \[x\] Another decoy' <<<"$decoy_ticked")"

oob="$(tick_item "$(f fresh.md)" 99)"
assert_eq "tick_item with an out-of-range index is a safe no-op (body unchanged)" \
  "$(f fresh.md)" "$oob"

# --- add_item (unused by loop.sh yet, needed by step 3b) --------------------

added="$(add_item "$(f fresh.md)" "Add a Go doc comment to Foo in bar.go.")"
assert_eq "add_item grows the checklist by exactly one" \
  6 "$(checklist_total "$added")"
assert_eq "add_item's new item lands last, after all 5 original items" \
  "- [ ] Add a Go doc comment to Foo in bar.go." \
  "$(_item_lines <<<"$added" | tail -1)"
assert_eq "add_item preserves the original 5 items unchanged and in order" \
  "$(_item_lines <<<"$(f fresh.md)")" \
  "$(_item_lines <<<"$added" | sed '$d')"
assert_eq "add_item preserves the state block untouched" \
  "$(_state_lines <<<"$(f fresh.md)")" \
  "$(_state_lines <<<"$added")"

added_no_state="$(add_item "$(f no_state.md)" "A third item.")"
assert_eq "add_item works on a body with no state block yet" \
  3 "$(checklist_total "$added_no_state")"

# --- is_finished -------------------------------------------------------

assert_status "is_finished is false on a fresh issue" 1 is_finished "$(f fresh.md)"
assert_status "is_finished is false midway" 1 is_finished "$(f midway.md)"
assert_status "is_finished is true when every item is checked" 0 is_finished "$(f finished.md)"

# --- state block: turn -------------------------------------------------

assert_eq "get_turn reads turn=0 from a fresh issue" \
  0 "$(get_turn "$(f fresh.md)")"
assert_eq "get_turn reads turn=2 from midway.md" \
  2 "$(get_turn "$(f midway.md)")"
assert_eq "get_turn defaults to 0 when there is no state block at all" \
  0 "$(get_turn "$(f no_state.md)")"

bumped="$(set_turn "$(f fresh.md)" 1)"
assert_eq "set_turn writes the new value" \
  1 "$(get_turn "$bumped")"
assert_eq "set_turn does not touch the checklist" \
  5 "$(checklist_total "$bumped")"

bumped_no_state="$(set_turn "$(f no_state.md)" 1)"
assert_eq "set_turn creates a state block when none exists" \
  1 "$(get_turn "$bumped_no_state")"
assert_eq "set_turn on a state-block-less body still preserves the checklist" \
  2 "$(checklist_total "$bumped_no_state")"

rebumped="$(set_turn "$(f midway.md)" 3)"
assert_eq "set_turn replaces an existing turn= line rather than duplicating it" \
  1 "$(grep -c '^turn=' <<<"$rebumped")"
assert_eq "set_turn on midway.md leaves attempts_item_2 alone" \
  1 "$(get_item_attempts "$rebumped" 2)"

# --- state block: per-item attempts -------------------------------------

assert_eq "get_item_attempts reads the existing counter on midway.md" \
  1 "$(get_item_attempts "$(f midway.md)" 2)"
assert_eq "get_item_attempts defaults to 0 for an item with no counter yet" \
  0 "$(get_item_attempts "$(f midway.md)" 3)"
assert_eq "get_item_attempts defaults to 0 with no state block at all" \
  0 "$(get_item_attempts "$(f no_state.md)" 1)"

incremented="$(set_item_attempts "$(f midway.md)" 2 2)"
assert_eq "set_item_attempts bumps item 2's counter" \
  2 "$(get_item_attempts "$incremented" 2)"
assert_eq "set_item_attempts does not create a counter for a different item" \
  0 "$(get_item_attempts "$incremented" 3)"
assert_eq "set_item_attempts leaves turn alone" \
  2 "$(get_turn "$incremented")"

first_fail="$(set_item_attempts "$(f fresh.md)" 1 1)"
assert_eq "set_item_attempts creates a new attempts_item_N line when none existed" \
  1 "$(get_item_attempts "$first_fail" 1)"
assert_eq "set_item_attempts on a fresh issue leaves the checklist untouched" \
  5 "$(checklist_total "$first_fail")"

# --- full turn simulation: tick + set_turn compose correctly --------------

body="$(f fresh.md)"
body="$(tick_item "$body" 1)"
body="$(set_turn "$body" 1)"
assert_eq "after turn 1 completes: item 1 checked" \
  1 "$(grep -c '^- \[x\] ' <<<"$body")"
assert_eq "after turn 1 completes: turn is 1" \
  1 "$(get_turn "$body")"
assert_eq "after turn 1 completes: first_unchecked_index now finds item 2" \
  2 "$(first_unchecked_index "$body")"
assert_status "after turn 1 completes: not finished (4 items left)" 1 is_finished "$body"

# drive it to completion
for i in 2 3 4 5; do
  body="$(tick_item "$body" "$i")"
  body="$(set_turn "$body" "$i")"
done
assert_status "after all 5 items ticked: is_finished is true" 0 is_finished "$body"
assert_eq "after all 5 items ticked: turn is 5" \
  5 "$(get_turn "$body")"

# --- phase (step 3b-1) ------------------------------------------------

assert_eq "get_phase defaults to discovery when absent and no checklist exists yet (fresh 3b issue)" \
  "discovery" "$(get_phase "Add expiry-date tracking to inventory items.")"
assert_eq "get_phase defaults to implement when absent but a checklist already exists (pre-existing 3a issue, back-compat)" \
  "implement" "$(get_phase "$(f fresh.md)")"
assert_eq "get_phase reads an explicit phase= over either default" \
  "plan" "$(get_phase "$(set_phase "$(f fresh.md)" plan)")"

phased="$(set_phase "Add expiry-date tracking to inventory items." discovery)"
assert_eq "set_phase writes phase= into a fresh state block" \
  "discovery" "$(get_phase "$phased")"
phased2="$(set_phase "$phased" plan)"
assert_eq "set_phase replaces an existing phase= rather than duplicating it" \
  1 "$(grep -c '^phase=' <<<"$phased2")"
assert_eq "set_phase transition reads back correctly" \
  "plan" "$(get_phase "$phased2")"

assert_eq "get_phase_attempts defaults to 0" \
  0 "$(get_phase_attempts "$phased" discovery)"
bumped_phase_attempts="$(set_phase_attempts "$phased" discovery 1)"
assert_eq "set_phase_attempts/get_phase_attempts round-trip" \
  1 "$(get_phase_attempts "$bumped_phase_attempts" discovery)"
assert_eq "phase attempt counters are independent per phase name" \
  0 "$(get_phase_attempts "$bumped_phase_attempts" plan)"
assert_eq "bumping the discovery attempt counter doesn't touch turn" \
  0 "$(get_turn "$bumped_phase_attempts")"

# --- plan_steps (step 3b-1) --------------------------------------------

assert_eq "plan_steps extracts exactly the checkbox lines under ## Steps, in order" \
  "- [ ] Add \`expiry_date\` (nullable date) to the \`inventory_items\` table via migrations/0047_item_expiry.up.sql and the matching .down.sql. The table already carries an RLS policy scoped to firm_id (see migrations/0012_inventory_items.up.sql) - no new policy needed, this is an added column only.
- [ ] Add \`ExpiryDate *time.Time\` to the \`Item\` struct in internal/inventory/items.go and update \`scanItem\`/the INSERT and UPDATE column lists to include it. Follow the existing nullable-column pattern already used for \`Notes\` in that same file.
- [ ] Add \`ExpiryDate\` to the JSON response shape in internal/inventory/handlers.go's item-serialization helper, following the existing field naming convention (camelCase) already used for the other fields there." \
  "$(plan_steps "$(f plan_wellformed.md)")"
assert_eq "plan_steps ignores non-Steps sections (Packages/Migration/CI Checks aren't checklist items)" \
  3 "$(plan_steps "$(f plan_wellformed.md)" | grep -c '^- \[')"

assert_status "plan_steps fails on a plan with no ## Steps heading at all" \
  1 plan_steps "$(f plan_malformed_no_steps.md)"
assert_status "plan_steps fails on a ## Steps section with zero checkbox lines" \
  1 plan_steps "$(f plan_malformed_empty_steps.md)"

assert_eq "plan_steps on a human-edited plan preserves the pre-checked step's checked state" \
  1 "$(plan_steps "$(f plan_human_edited.md)" | grep -c '^- \[x\]')"
assert_eq "plan_steps on a human-edited plan reflects Kaan's reworded text verbatim, not the original" \
  1 "$(plan_steps "$(f plan_human_edited.md)" | grep -c 'repository test fixtures need updating')"

# --- plan_migration_section (step 3b-1 follow-up: issue #80's migration- --
# --- number collision) --------------------------------------------------

assert_eq "plan_migration_section extracts the ## Migration section's text verbatim" \
  "migrations/0047_item_expiry.up.sql / .down.sql" \
  "$(plan_migration_section "$(f plan_wellformed.md)")"
assert_eq "plan_migration_section returns nothing for a plan with no ## Migration heading" \
  "" "$(plan_migration_section "$(f plan_malformed_no_steps.md)")"

# --- set_checklist_from_lines (step 3b-1) -------------------------------

feature_body="Add expiry-date tracking to inventory items.

<!-- agent-state
phase=awaiting-plan
turn=2
-->"
steps="$(plan_steps "$(f plan_wellformed.md)")"
populated="$(set_checklist_from_lines "$feature_body" "$steps")"
assert_eq "set_checklist_from_lines creates the ## Checklist heading when absent" \
  3 "$(checklist_total "$populated")"
assert_eq "set_checklist_from_lines preserves the existing state block untouched" \
  "awaiting-plan" "$(get_phase "$populated")"
assert_eq "set_checklist_from_lines preserves turn" \
  2 "$(get_turn "$populated")"
assert_status "the populated checklist is not yet finished (nothing ticked)" \
  1 is_finished "$populated"

with_stale="## Checklist
- [ ] stale item that should be replaced

<!-- agent-state
turn=1
-->"
replaced="$(set_checklist_from_lines "$with_stale" "$steps")"
assert_eq "set_checklist_from_lines replaces an already-present (defensive-path) checklist wholesale" \
  3 "$(checklist_total "$replaced")"
assert_eq "... and the stale item is gone" \
  0 "$(grep -c 'stale item' <<<"$replaced")"

# --- extract_last_reply (step 3b-1) -------------------------------------

chat_history=$'\n# aider chat started at 2026-09-01 10:00:00\n\n#### For the feature described in this issue:\n  \n#### Add expiry-date tracking to inventory items.\n  \n#### Answer: which packages, which patterns, which tables/migrations, next migration number, which CLAUDE.md rules apply.\n\nPackages involved: internal/inventory (Item struct, handlers.go).\n\nNext migration number: 0047 (last is migrations/0046_*).\n\nCLAUDE.md rule 3 (RLS) applies since inventory_items is firm-scoped.\n\n'
reply="$(extract_last_reply "$chat_history")"
assert_eq "extract_last_reply gets the assistant's answer, not the echoed prompt" \
  "Packages involved: internal/inventory (Item struct, handlers.go).

Next migration number: 0047 (last is migrations/0046_*).

CLAUDE.md rule 3 (RLS) applies since inventory_items is firm-scoped." \
  "$reply"
assert_eq "extract_last_reply's output does not contain any of the prompt's #### lines" \
  0 "$(grep -c '^#### ' <<<"$reply")"

empty_history=$'\n# aider chat started at 2026-09-01 10:00:00\n\n#### A prompt with no reply yet (e.g. the call failed before any response).\n'
assert_eq "extract_last_reply on a prompt with no assistant reply returns empty" \
  "" "$(extract_last_reply "$empty_history")"

# --- slugify / plan_filename / plan_file state (step 3b-1) -------------

assert_eq "slugify lowercases and hyphenates" \
  "add-expiry-date-tracking-to-inventory-items" \
  "$(slugify "Add expiry-date tracking to inventory items")"
assert_eq "slugify collapses punctuation runs into single hyphens and trims ends" \
  "fix-n-1-query-urgent-do-it-now" \
  "$(slugify "Fix N+1 query!! (urgent) — do it NOW")"
assert_eq "slugify caps length at 60 chars and doesn't leave a trailing hyphen at the cut" \
  60 "$(slugify "$(printf 'word %.0s' {1..30})" | wc -c | tr -d ' ')"

assert_eq "plan_filename composes docs/plans/<number>-<slug>.md" \
  "docs/plans/0001-add-expiry-date-tracking-to-inventory-items.md" \
  "$(plan_filename "0001" "Add expiry-date tracking to inventory items")"

state_body="Feature description.

<!-- agent-state
phase=plan
turn=1
-->"
assert_eq "get_plan_file is empty before one is claimed" \
  "" "$(get_plan_file "$state_body")"
claimed="$(set_plan_file "$state_body" "docs/plans/0001-add-expiry-date-tracking.md")"
assert_eq "set_plan_file/get_plan_file round-trip" \
  "docs/plans/0001-add-expiry-date-tracking.md" "$(get_plan_file "$claimed")"
assert_eq "set_plan_file leaves phase alone" \
  "plan" "$(get_phase "$claimed")"
reclaimed="$(set_plan_file "$claimed" "docs/plans/0002-different.md")"
assert_eq "set_plan_file replaces rather than duplicating the key" \
  1 "$(grep -c '^plan_file=' <<<"$reclaimed")"


# --- quota backoff state (issue #81 fix) ------------------------------------

assert_eq "get_quota_streak defaults to 0 when unset" \
  0 "$(get_quota_streak "$state_body")"
streaked="$(set_quota_streak "$state_body" 2)"
assert_eq "set_quota_streak/get_quota_streak round-trip" \
  2 "$(get_quota_streak "$streaked")"
assert_eq "set_quota_streak leaves phase alone" \
  "plan" "$(get_phase "$streaked")"
restreaked="$(set_quota_streak "$streaked" 3)"
assert_eq "set_quota_streak replaces rather than duplicating the key" \
  1 "$(grep -c '^quota_streak=' <<<"$restreaked")"
assert_eq "set_quota_streak replaces the value in place" \
  3 "$(get_quota_streak "$restreaked")"

assert_eq "get_quota_backoff_until is empty before one is set" \
  "" "$(get_quota_backoff_until "$state_body")"
backed_off="$(set_quota_backoff_until "$state_body" "2026-09-04T17:28:00Z")"
assert_eq "set_quota_backoff_until/get_quota_backoff_until round-trip" \
  "2026-09-04T17:28:00Z" "$(get_quota_backoff_until "$backed_off")"
assert_eq "set_quota_backoff_until leaves turn alone" \
  1 "$(get_turn "$backed_off")"

both="$(set_quota_backoff_until "$streaked" "2026-09-04T17:28:00Z")"
assert_eq "quota_streak and quota_backoff_until coexist in the same state block" \
  2 "$(get_quota_streak "$both")"
assert_eq "... and quota_backoff_until is readable too" \
  "2026-09-04T17:28:00Z" "$(get_quota_backoff_until "$both")"

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
