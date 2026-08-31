#!/usr/bin/env bash
# Offline test for set_issue_body's verify-after-write logic in loop.sh,
# against a mocked `gh` (never touches the network). This is the direct
# response to issue #73's validation chain: every run's turn write
# silently didn't take, and nothing caught it because a successful
# `gh issue edit` doesn't mean the write actually stuck.
#
# What this proves: given a `gh issue edit` that reports success but a
# `gh issue view` read-back that shows the write didn't take (exactly
# what we saw on #73, simulated here rather than reproduced - the real
# round-trip bug is not offline-testable, see test_gh_or_block.sh's
# header for why), set_issue_body detects the mismatch, attempts
# block_issue, and exits non-zero - it does not silently proceed as if
# the write succeeded. It also proves the matching-write path stays
# silent (no false positives on ordinary success).
#
# What this does NOT prove: why the real write is failing. That still
# needs reproduction against the live gh CLI.
#
# Usage: .github/agent/tests/test_state_verification.sh

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

# $1: calls-log path. $2: the body gh's mocked "issue view" should
# return (simulating what the round-trip read finds - independent of
# what was actually "written", exactly modelling a write that silently
# didn't take). Calls set_issue_body with a body carrying turn=3.
run_scenario() {
  local calls_log="$1" readback_body="$2"
  : >"$calls_log"
  (
    set -uo pipefail
    gh() {
      printf 'CALL: %s\n' "$*" >>"$CALLS_LOG"
      case "$1 $2" in
        "issue view")
          printf '%s' "$READBACK_BODY"
          return 0
          ;;
        "issue edit"|"issue comment")
          return 0
          ;;
        *)
          return 0
          ;;
      esac
    }
    export CALLS_LOG="$calls_log"
    export READBACK_BODY="$readback_body"
    export GITHUB_REPOSITORY="acme/example"
    export GITHUB_RUN_ID="999999"
    # shellcheck source=../loop.sh
    source "$LOOP_SH"
    ISSUE_NUMBER=73
    body='## Checklist
- [x] item one
- [ ] item two

<!-- agent-state
turn=3
-->'
    set_issue_body 73 "$body"
    rc=$?
    print_resolved_state
    exit "$rc"
  ) 2>&1
}

# --- Scenario 1: write silently does not take (the #73 case, simulated) ----
# gh issue edit reports success, but the round-trip read shows turn=0
# (no state block) - modelling exactly what #73's real runs showed.

readback_no_state='## Checklist
- [x] item one
- [ ] item two
'
out1="$(run_scenario /tmp/state_verify_calls_1.log "$readback_no_state")"
rc1=$?

assert "scenario 1: set_issue_body exits non-zero when the round-trip doesn't match" \
  "$([ "$rc1" -ne 0 ] && echo 1 || echo 0)"
assert_contains "scenario 1: the mismatch is reported explicitly (wrote vs read back)" \
  "$out1" "wrote turn=3 but reading the issue back shows turn=0"
assert_contains "scenario 1: block_issue was attempted (issue edit for labels)" \
  "$(cat /tmp/state_verify_calls_1.log)" "issue edit 73 --repo acme/example --remove-label agent:go --add-label agent:blocked"
assert_contains "scenario 1: block_issue's comment was attempted" \
  "$(cat /tmp/state_verify_calls_1.log)" "issue comment 73"
assert_contains "scenario 1: the blocking comment names the real problem, not a generic one" \
  "$(cat /tmp/state_verify_calls_1.log)" "state verification failed"
assert_contains "scenario 1: RESOLVED STATE reflects the specific failure mode" \
  "$out1" "action=state_verification_failed"
assert_contains "scenario 1: RESOLVED STATE reflects retrigger was suppressed" \
  "$out1" "retrigger=false"

# --- Scenario 2: write genuinely takes (the common case) -------------------

readback_matching='## Checklist
- [x] item one
- [ ] item two

<!-- agent-state
turn=3
-->'
out2="$(run_scenario /tmp/state_verify_calls_2.log "$readback_matching")"
rc2=$?

assert "scenario 2: set_issue_body succeeds silently when the round-trip matches" \
  "$([ "$rc2" -eq 0 ] && echo 1 || echo 0)"
assert "scenario 2: block_issue is never invoked (only issue edit + issue view = 2 calls)" \
  "$([ "$(grep -c '^CALL: ' /tmp/state_verify_calls_2.log)" -eq 2 ] && echo 1 || echo 0)"
assert_contains "scenario 2: no false-positive verification-failure message" \
  "$out2" "action=unknown"

rm -f /tmp/state_verify_calls_1.log /tmp/state_verify_calls_2.log

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
