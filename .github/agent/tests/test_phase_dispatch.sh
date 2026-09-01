#!/usr/bin/env bash
# Offline test for main()'s phase dispatch in loop.sh: given a phase=
# value in the issue's state block, does main() call the right phase
# function? Tests the DISPATCH decision only, decoupled from what each
# phase function actually does (those have no offline seam of their own
# - they're gh/git/aider orchestration - which is exactly why this test
# stubs them out rather than trying to exercise them for real).
#
# Works by sourcing loop.sh against a mocked `gh` (never touches the
# network), then overriding run_discovery_phase/run_plan_phase/
# run_awaiting_plan_phase/run_implement_phase with stubs that just
# record which one fired, before calling main().
#
# Usage: .github/agent/tests/test_phase_dispatch.sh

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOOP_SH="$HERE/../loop.sh"

pass=0
fail=0

assert_contains() {
  local desc="$1" haystack="$2" needle="$3"
  if [[ "$haystack" == *"$needle"* ]]; then
    pass=$((pass + 1))
    echo "ok   - $desc"
  else
    fail=$((fail + 1))
    echo "FAIL - $desc"
    echo "       expected to find: $(printf '%q' "$needle")"
    echo "       in: $(printf '%q' "$haystack")"
  fi
}

assert_not_contains() {
  local desc="$1" haystack="$2" needle="$3"
  if [[ "$haystack" != *"$needle"* ]]; then
    pass=$((pass + 1))
    echo "ok   - $desc"
  else
    fail=$((fail + 1))
    echo "FAIL - $desc"
    echo "       expected NOT to find: $(printf '%q' "$needle")"
  fi
}

# $1: the issue body (whatever phase= it carries, or none for the
# back-compat default paths). $2 (optional): a calls-log file path to
# capture block_issue's otherwise-suppressed gh calls to.
run_scenario() {
  local body="$1" calls_log="${2:-/dev/null}"
  : >"$calls_log"
  (
    set -uo pipefail
    gh() {
      case "$1 $2" in
        "run list")
          if [[ "$*" == *"createdAt"* ]]; then
            echo 0 # guard_run_rate: nothing in the window
          fi
          # concurrency's run-list query wants an empty result, i.e. no
          # output at all - the default no-op below already gives that.
          return 0
          ;;
        "issue view")
          if [[ "$*" == *"--json body"* ]]; then
            printf '%s' "$MOCK_BODY"
          elif [[ "$*" == *"--json labels"* ]]; then
            echo "agent:go"
          fi
          return 0
          ;;
        "issue comment")
          # Log to a file, not stdout: block_issue calls this directly
          # (not through gh_or_block, by design - see its own comment)
          # and redirects both stdout and stderr of the call to
          # /dev/null, so nothing written to either stream survives for
          # a test to observe.
          echo "CALL: $*" >>"$CALLS_LOG"
          return 0
          ;;
        *)
          return 0
          ;;
      esac
    }
    export MOCK_BODY="$body"
    export CALLS_LOG="$calls_log"
    export GITHUB_REPOSITORY="acme/example"
    export GITHUB_RUN_ID="999999"
    export INPUT_ISSUE="42"
    # shellcheck source=../loop.sh
    source "$LOOP_SH"
    trap - EXIT

    run_discovery_phase() { echo "DISPATCHED:discovery"; }
    run_plan_phase() { echo "DISPATCHED:plan"; }
    run_awaiting_plan_phase() { echo "DISPATCHED:awaiting-plan"; }
    run_implement_phase() { echo "DISPATCHED:implement"; }

    main
  ) 2>&1
}

state_block() {
  printf '\n\n<!-- agent-state\nphase=%s\nturn=1\n-->' "$1"
}

# --- explicit phase= wins over any default -------------------------------

out="$(run_scenario "Add a feature.$(state_block discovery)")"
assert_contains "explicit phase=discovery dispatches to discovery" "$out" "DISPATCHED:discovery"
assert_not_contains "... and only discovery" "$out" "DISPATCHED:plan"

out="$(run_scenario "Add a feature.$(state_block plan)")"
assert_contains "explicit phase=plan dispatches to plan" "$out" "DISPATCHED:plan"

out="$(run_scenario "Add a feature.$(state_block awaiting-plan)")"
assert_contains "explicit phase=awaiting-plan dispatches to awaiting-plan" "$out" "DISPATCHED:awaiting-plan"

out="$(run_scenario "## Checklist
- [ ] some item
$(state_block implement)")"
assert_contains "explicit phase=implement dispatches to implement" "$out" "DISPATCHED:implement"

# --- back-compat defaults (no phase= at all) ------------------------------

out="$(run_scenario "Add a feature, no checklist, no state block yet.")"
assert_contains "no phase, no checklist: back-compat default is discovery (fresh 3b issue)" \
  "$out" "DISPATCHED:discovery"

out="$(run_scenario "## Checklist
- [ ] some pre-existing 3a item
- [x] another one")"
assert_contains "no phase, but a checklist already exists: back-compat default is implement (pre-existing 3a issue keeps working untouched)" \
  "$out" "DISPATCHED:implement"

# --- unknown phase blocks rather than dispatching anywhere ----------------

out="$(run_scenario "Add a feature.$(state_block bogus-phase-name)" /tmp/phase_dispatch_calls.log)"
assert_not_contains "an unknown phase value dispatches to nothing" "$out" "DISPATCHED:"
assert_contains "... and is reported as an unknown-phase condition, via block_issue's comment" \
  "$(cat /tmp/phase_dispatch_calls.log)" "unknown phase 'bogus-phase-name'"
rm -f /tmp/phase_dispatch_calls.log

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
