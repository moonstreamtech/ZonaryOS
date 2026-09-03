#!/usr/bin/env bash
# Offline test for issue #81's quota-backoff machinery in loop.sh:
# record_quota_exhaustion (streak tracking + the MAX_QUOTA_STREAK block)
# and quota_backoff_active (the main()-entry skip). Against a mocked `gh`
# (never touches the network) - same pattern as test_state_verification.sh,
# extended to capture the --body an `issue edit` call carries so a
# round-tripping `issue view` can hand it back, since record_quota_
# exhaustion goes through the real set_issue_body (verify-after-write and
# all).
#
# What this proves: a confirmed quota exhaustion is recorded (streak
# incremented, a 24h backoff deadline written) and reported via the usual
# "will retry automatically" comment, UNTIL MAX_QUOTA_STREAK consecutive
# exhaustions have piled up with zero successful calls between them -
# at which point the issue is blocked instead of left retrying forever
# (the exact failure mode issue #81 hit: 8 identical comments over 24+
# hours, with nothing that would ever stop). It also proves quota_backoff_
# active correctly reads a future-vs-past deadline out of state.
#
# Usage: .github/agent/tests/test_quota_backoff.sh

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
    echo "       did not expect to find: $(printf '%q' "$needle")"
    echo "       in: $(printf '%q' "$haystack")"
  fi
}

# $1: calls-log path. $2: body-store path (round-tripped by the mocked
# `gh issue edit`/`gh issue view`, seeded with $3). $4: TURN to set.
# Calls record_quota_exhaustion discovery and prints RESOLVED STATE.
run_scenario() {
  local calls_log="$1" body_store="$2" seed_body="$3" turn="$4"
  : >"$calls_log"
  printf '%s' "$seed_body" >"$body_store"
  (
    set -uo pipefail
    gh() {
      printf 'CALL: %s\n' "$*" >>"$CALLS_LOG"
      case "$1 $2" in
        "issue edit")
          local args=("$@") i
          for ((i = 0; i < ${#args[@]}; i++)); do
            if [ "${args[$i]}" = "--body" ]; then
              printf '%s' "${args[$((i + 1))]}" >"$BODY_STORE"
            fi
          done
          return 0
          ;;
        "issue view")
          cat "$BODY_STORE"
          return 0
          ;;
        "issue comment")
          return 0
          ;;
        *)
          return 0
          ;;
      esac
    }
    export CALLS_LOG="$calls_log"
    export BODY_STORE="$body_store"
    export GITHUB_REPOSITORY="acme/example"
    export GITHUB_RUN_ID="999999"
    # shellcheck source=../loop.sh
    source "$LOOP_SH"
    ISSUE_NUMBER=81
    TURN="$turn"
    body="$(cat "$BODY_STORE")"
    record_quota_exhaustion discovery
    rc=$?
    print_resolved_state
    exit "$rc"
  ) 2>&1
  echo "EXIT:$?"
}

fresh_body='Feature description.

<!-- agent-state
phase=discovery
turn=0
-->'

# --- First exhaustion: streak 0 -> 1, backs off, does not block ------------

out1="$(run_scenario /tmp/quota_calls_1.log /tmp/quota_body_1.txt "$fresh_body" 1)"
assert_contains "1st exhaustion: posts the usual retry-automatically comment" \
  "$(cat /tmp/quota_calls_1.log)" "issue comment 81"
assert_not_contains "1st exhaustion: does not swap agent:go for agent:blocked" \
  "$(cat /tmp/quota_calls_1.log)" "--add-label agent:blocked"
assert_contains "1st exhaustion: RESOLVED STATE reports budget_exhausted" \
  "$out1" "action=budget_exhausted"
assert_contains "1st exhaustion: RESOLVED STATE suppresses retrigger" \
  "$out1" "retrigger=false"
body1="$(cat /tmp/quota_body_1.txt)"
assert_contains "1st exhaustion: streak is now 1 in the persisted body" \
  "$body1" "quota_streak=1"
assert_contains "1st exhaustion: a backoff deadline was recorded" \
  "$body1" "quota_backoff_until="

# --- Second exhaustion, streak carried forward: 1 -> 2, still backs off ----

seed2="Feature description.

<!-- agent-state
phase=discovery
turn=1
quota_streak=1
-->"
out2="$(run_scenario /tmp/quota_calls_2.log /tmp/quota_body_2.txt "$seed2" 2)"
body2="$(cat /tmp/quota_body_2.txt)"
assert_contains "2nd exhaustion: streak is now 2" "$body2" "quota_streak=2"
assert_not_contains "2nd exhaustion (below MAX_QUOTA_STREAK=3): still not blocked" \
  "$(cat /tmp/quota_calls_2.log)" "--add-label agent:blocked"

# --- Third exhaustion in a row: hits MAX_QUOTA_STREAK, blocks the issue ----

seed3="Feature description.

<!-- agent-state
phase=discovery
turn=2
quota_streak=2
-->"
out3="$(run_scenario /tmp/quota_calls_3.log /tmp/quota_body_3.txt "$seed3" 3)"
assert_contains "3rd exhaustion (hits MAX_QUOTA_STREAK=3): swaps agent:go for agent:blocked" \
  "$(cat /tmp/quota_calls_3.log)" "--add-label agent:blocked"
assert_contains "3rd exhaustion: the blocking comment explains it's a persistent problem, not rate-limiting" \
  "$(cat /tmp/quota_calls_3.log)" "3 times in a row"
assert_contains "3rd exhaustion: RESOLVED STATE reports the block, not budget_exhausted" \
  "$out3" "action=quota_exhaustion_blocked"

# --- quota_backoff_active reads a future vs. past deadline correctly -------

(
  set -uo pipefail
  gh() { return 0; }
  export GITHUB_REPOSITORY="acme/example"
  export GITHUB_RUN_ID="999999"
  # shellcheck source=../loop.sh
  source "$LOOP_SH"
  body="Feature description.

<!-- agent-state
quota_backoff_until=2999-01-01T00:00:00Z
-->"
  if quota_backoff_active; then
    echo "FUTURE:active"
  else
    echo "FUTURE:inactive"
  fi

  body="Feature description.

<!-- agent-state
quota_backoff_until=2000-01-01T00:00:00Z
-->"
  if quota_backoff_active; then
    echo "PAST:active"
  else
    echo "PAST:inactive"
  fi

  body="Feature description.

<!-- agent-state
turn=0
-->"
  if quota_backoff_active; then
    echo "UNSET:active"
  else
    echo "UNSET:inactive"
  fi
) >/tmp/quota_backoff_active.log 2>&1
out4="$(cat /tmp/quota_backoff_active.log)"
assert_contains "quota_backoff_active: a deadline far in the future is still active" \
  "$out4" "FUTURE:active"
assert_contains "quota_backoff_active: a deadline in the past is no longer active" \
  "$out4" "PAST:inactive"
assert_contains "quota_backoff_active: no deadline at all means not active" \
  "$out4" "UNSET:inactive"

rm -f /tmp/quota_calls_1.log /tmp/quota_body_1.txt /tmp/quota_calls_2.log /tmp/quota_body_2.txt \
  /tmp/quota_calls_3.log /tmp/quota_body_3.txt /tmp/quota_backoff_active.log

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
