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

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
