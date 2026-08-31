#!/usr/bin/env bash
# Offline test for guard_run_rate's own control flow in loop.sh, against
# a mocked `gh run list` (never touches the network, and does not
# re-implement gh's jq date filtering - that's gh's contract, not ours;
# the mock just returns a count directly, so this tests only what this
# function does with that count).
#
# Usage: .github/agent/tests/test_guard_run_rate.sh

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOOP_SH="$HERE/../loop.sh"

pass=0
fail=0

assert() {
  local desc="$1" ok="$2"
  if [ "$ok" = "1" ]; then
    pass=$((pass + 1))
    echo "ok   - $desc"
  else
    fail=$((fail + 1))
    echo "FAIL - $desc"
  fi
}

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

# $1: the count the mocked `gh run list` should report.
run_scenario() {
  local mock_count="$1"
  (
    set -uo pipefail
    gh() {
      if [ "$1 $2" = "run list" ]; then
        echo "$MOCK_COUNT"
        return 0
      fi
      return 0
    }
    export MOCK_COUNT="$mock_count"
    export GITHUB_REPOSITORY="acme/example"
    export GITHUB_RUN_ID="999999"
    # shellcheck source=../loop.sh
    source "$LOOP_SH"
    guard_run_rate
    echo "guard_run_rate returned without exiting"
  ) 2>&1
  echo "EXIT:$?"
}

# --- Under threshold: guard_run_rate returns normally, main() would continue

out1="$(run_scenario 5)"
assert_contains "under threshold (5 runs): guard_run_rate returns rather than exiting" \
  "$out1" "guard_run_rate returned without exiting"
assert_contains "under threshold: exits 0 (the subshell's own natural exit, not a brake firing)" \
  "$out1" "EXIT:0"

# --- At/over threshold: guard_run_rate stops the run --------------------

out2="$(run_scenario 30)"
assert_contains "at threshold (30 runs, threshold is 30): refuses to proceed" \
  "$out2" "Refusing to proceed"
if [[ "$out2" == *"guard_run_rate returned without exiting"* ]]; then
  fail=$((fail + 1))
  echo "FAIL - at threshold: guard_run_rate must exit, not return"
else
  pass=$((pass + 1))
  echo "ok   - at threshold: guard_run_rate exits rather than returning"
fi
assert_contains "at threshold: exits cleanly (0), not as an error - this is an expected brake, not a crash" \
  "$out2" "EXIT:0"

out3="$(run_scenario 100)"
assert_contains "well over threshold (100 runs): still refuses to proceed" \
  "$out3" "Refusing to proceed"

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
