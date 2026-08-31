#!/usr/bin/env bash
# Offline test for gh_or_block/block_issue's FAILURE-HANDLING CONTROL FLOW
# in loop.sh - not a test of the live GitHub API. It works by defining a
# fake `gh` shell function (never touches the network) before sourcing
# loop.sh, then calling gh_or_block directly and asserting on its exit
# code, output, and how many times the fake `gh` was invoked.
#
# What this does NOT test: whether state genuinely survives a round trip
# through the real `gh issue edit`/`gh issue view` API. That is not
# offline-testable with this architecture - loop.sh's entire reason for
# being separate from lib.sh is that its correctness depends on gh/git/
# GitHub's actual behavior, which a fixture can't stand in for. What IS
# testable, and what this covers, is the promise this PR adds: a failed
# gh mutation always attempts block_issue and always exits non-zero
# reporting the ORIGINAL failure, and a block_issue that ALSO fails
# degrades to loud logging instead of recursing.
#
# Usage: .github/agent/tests/test_gh_or_block.sh

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

# Runs gh_or_block (against a mocked gh whose behavior is controlled by
# GH_MOCK_FAIL_ORIGINAL / GH_MOCK_FAIL_BLOCK) in a subshell, since
# gh_or_block calls `exit` on failure and we don't want that to kill this
# test runner. $1: description-only tag for the call-log file. $2..: the
# gh_or_block arguments to attempt (a fake mutation).
run_scenario() {
  local calls_log="$1"
  shift
  : >"$calls_log"
  (
    set -uo pipefail
    gh() {
      echo "$*" >>"$CALLS_LOG"
      case "$1 $2" in
        "issue edit")
          if [ "${GH_MOCK_FAIL_ORIGINAL:-0}" = "1" ] && [[ "$*" != *"--add-label agent:blocked"* ]]; then
            echo "mock: original gh issue edit failed" >&2
            return 1
          fi
          if [[ "$*" == *"--add-label agent:blocked"* ]] && [ "${GH_MOCK_FAIL_BLOCK:-0}" = "1" ]; then
            echo "mock: block's own gh issue edit failed too" >&2
            return 1
          fi
          return 0
          ;;
        "issue comment")
          if [ "${GH_MOCK_FAIL_BLOCK:-0}" = "1" ]; then
            echo "mock: block's own gh issue comment failed too" >&2
            return 1
          fi
          return 0
          ;;
        *)
          return 0
          ;;
      esac
    }
    export CALLS_LOG="$calls_log"
    export GITHUB_REPOSITORY="acme/example"
    export GITHUB_RUN_ID="999999"
    # shellcheck source=../loop.sh
    source "$LOOP_SH"
    ISSUE_NUMBER=42
    gh_or_block "$@"
    rc=$?
    print_resolved_state
    exit "$rc"
  ) 2>&1
}

# --- Scenario 1: original gh call fails, block_issue succeeds --------------

GH_MOCK_FAIL_ORIGINAL=1 GH_MOCK_FAIL_BLOCK=0 \
  out1="$(run_scenario /tmp/gh_or_block_calls_1.log issue edit 42 --repo acme/example --body "some new body")"
rc1=$?

assert "scenario 1: gh_or_block exits non-zero when the original gh call fails" \
  "$([ "$rc1" -ne 0 ] && echo 1 || echo 0)"
assert_contains "scenario 1: the ORIGINAL error is present in the output" \
  "$out1" "mock: original gh issue edit failed"
assert_contains "scenario 1: gh_or_block reports the exact failing command" \
  "$out1" "gh command failed"
assert_contains "scenario 1: gh_or_block reports the exact failing command" \
  "$out1" "issue edit 42 --repo acme/example"
assert "scenario 1: block_issue was attempted (label edit + comment = 2 more gh calls beyond the original)" \
  "$([ "$(wc -l </tmp/gh_or_block_calls_1.log)" -eq 3 ] && echo 1 || echo 0)"
assert_contains "scenario 1: block_issue's label swap was actually attempted" \
  "$(cat /tmp/gh_or_block_calls_1.log)" "--add-label agent:blocked"
assert_contains "scenario 1: block_issue's comment was actually attempted" \
  "$(cat /tmp/gh_or_block_calls_1.log)" "issue comment 42"
assert_contains "scenario 1: RESOLVED STATE reflects the failure classification" \
  "$out1" "action=gh_command_failed"
assert_contains "scenario 1: RESOLVED STATE reflects retrigger was suppressed" \
  "$out1" "retrigger=false"
assert_contains "scenario 1: RESOLVED STATE records the last gh command attempted" \
  "$out1" "last_gh_cmd=[gh issue edit 42 --repo acme/example --body some new body]"

# --- Scenario 2: original AND block_issue's own calls all fail -------------
# This is the "token/permission problem" case the review flagged: prove
# it degrades to loud logging, not recursion, and the ORIGINAL error is
# still what's reported (not masked by block_issue's failure).

GH_MOCK_FAIL_ORIGINAL=1 GH_MOCK_FAIL_BLOCK=1 \
  out2="$(run_scenario /tmp/gh_or_block_calls_2.log issue edit 42 --repo acme/example --body "some new body")"
rc2=$?

assert "scenario 2: still exits non-zero" \
  "$([ "$rc2" -ne 0 ] && echo 1 || echo 0)"
assert_contains "scenario 2: the ORIGINAL error is still present" \
  "$out2" "mock: original gh issue edit failed"
assert_contains "scenario 2: block_issue's own edit failure is logged loudly, not silently swallowed" \
  "$out2" "could not swap labels on #42"
assert_contains "scenario 2: block_issue's own comment failure is logged loudly too" \
  "$out2" "could not post the blocking comment on #42"
assert "scenario 2: exactly 3 gh calls total - original + block's edit + block's comment, no recursion" \
  "$([ "$(wc -l </tmp/gh_or_block_calls_2.log)" -eq 3 ] && echo 1 || echo 0)"
# The strongest evidence against recursion: this scenario completed at
# all (no hang) and made a small, bounded number of gh calls rather than
# looping. If gh_or_block's failure path called itself instead of the
# raw gh calls block_issue makes, this count would grow without bound.

# --- Scenario 3: success path is a plain pass-through -----------------------

GH_MOCK_FAIL_ORIGINAL=0 GH_MOCK_FAIL_BLOCK=0 \
  out3="$(run_scenario /tmp/gh_or_block_calls_3.log issue edit 42 --repo acme/example --body "some new body")"
rc3=$?

assert "scenario 3: exits zero when the underlying gh call succeeds" \
  "$([ "$rc3" -eq 0 ] && echo 1 || echo 0)"
assert "scenario 3: block_issue is never invoked on the success path (only the 1 original call)" \
  "$([ "$(wc -l </tmp/gh_or_block_calls_3.log)" -eq 1 ] && echo 1 || echo 0)"
assert_not_contains "scenario 3: no 'gh command failed' noise on success" \
  "$out3" "gh command failed"
assert_contains "scenario 3: RESOLVED STATE still records the last gh command attempted, even on success" \
  "$out3" "last_gh_cmd=[gh issue edit 42 --repo acme/example --body some new body]"

rm -f /tmp/gh_or_block_calls_1.log /tmp/gh_or_block_calls_2.log /tmp/gh_or_block_calls_3.log

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
