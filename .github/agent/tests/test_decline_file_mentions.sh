#!/usr/bin/env bash
# Offline test for _run_aider_readonly_or_editing's decline_file_mentions
# argument (the discovery-only TPM fix - see run_aider_discovery's own
# comment in loop.sh for the full story). Does not invoke real aider or
# touch the network: a fake `aider` reads one line from its own stdin and
# echoes exactly what it got, so this proves the piping mechanism itself
# - "true" feeds it a real line ("n"), "false" leaves its stdin exactly
# as this process's own (nothing piped in, i.e. whatever /dev/null or a
# closed fd this test harness already has) - without needing the real
# aider binary or a live confirm_ask() round trip (that was verified
# separately, by hand, against aider 0.86.2's actual InputOutput class).
#
# Usage: .github/agent/tests/test_decline_file_mentions.sh

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOOP_SH="$HERE/../loop.sh"

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

run_scenario() {
  local decline="$1"
  (
    set -uo pipefail
    export GITHUB_REPOSITORY="acme/example"
    export GITHUB_RUN_ID="999999"
    # shellcheck source=../loop.sh
    source "$LOOP_SH"
    fake_aider() {
      # Mirrors what a real confirm_ask() prompt would see: read exactly
      # one line from stdin, or report that there was nothing to read
      # (matching the EOF-default-yes path when nothing is piped in).
      local line
      if IFS= read -r line; then
        echo "STDIN_LINE:$line"
      else
        echo "STDIN_EOF"
      fi
    }
    _run_aider_readonly_or_editing "$decline" fake_aider </dev/null
  ) 2>&1
}

out_true="$(run_scenario true)"
assert_eq "decline_file_mentions=true: the command receives a piped 'n' line, not EOF" \
  "1" "$([[ "$out_true" == *"STDIN_LINE:n"* ]] && echo 1 || echo 0)"

out_false="$(run_scenario false)"
assert_eq "decline_file_mentions=false: the command's own stdin is left alone (EOF here, same as production's empty stdin)" \
  "1" "$([[ "$out_false" == *"STDIN_EOF"* ]] && echo 1 || echo 0)"

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
