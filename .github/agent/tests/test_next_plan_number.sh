#!/usr/bin/env bash
# Offline test for loop.sh's next_plan_number(), against a real (but
# temporary, throwaway) docs/plans/ directory - no network, no gh, no
# aider. This is filesystem-touching, which is why it lives in loop.sh
# rather than lib.sh (lib.sh's whole contract is staying filesystem- and
# network-free) - but the filesystem it touches here is a tempdir this
# test creates and destroys, not the real repo.
#
# Usage: .github/agent/tests/test_next_plan_number.sh

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

# Runs next_plan_number() with $PWD set to a fresh tempdir whose
# docs/plans/ is populated per $1 (a newline-separated list of filenames
# to create, empty for "directory doesn't exist at all").
run_scenario() {
  local files="$1"
  local tmp
  tmp="$(mktemp -d)"
  (
    cd "$tmp" || exit 1
    if [ -n "$files" ]; then
      mkdir -p docs/plans
      while IFS= read -r f; do
        [ -n "$f" ] && touch "docs/plans/$f"
      done <<<"$files"
    fi
    export GITHUB_REPOSITORY="acme/example"
    export GITHUB_RUN_ID="999999"
    gh() { return 0; }
    # shellcheck source=../loop.sh
    source "$LOOP_SH"
    trap - EXIT # loop.sh installs a print_resolved_state EXIT trap on
                # sourcing; not relevant to this test and would otherwise
                # contaminate next_plan_number's captured stdout.
    next_plan_number
  )
  rm -rf "$tmp"
}

assert_eq "no docs/plans/ directory at all: starts at 0001" \
  "0001" "$(run_scenario "")"

assert_eq "empty docs/plans/ directory: starts at 0001" \
  "0001" "$(run_scenario $'')"

assert_eq "one existing plan (0001-foo.md): next is 0002" \
  "0002" "$(run_scenario $'0001-foo.md')"

assert_eq "several existing plans, out of order on disk: picks the highest, not the last-created" \
  "0043" "$(run_scenario $'0007-bar.md\n0042-baz.md\n0003-qux.md')"

assert_eq "zero-pads to 4 digits past 9" \
  "0010" "$(run_scenario $'0009-foo.md')"

assert_eq "does not get confused by a non-numeric-prefixed file in the directory" \
  "0005" "$(run_scenario $'README.md\n0004-foo.md')"

assert_eq "handles a 4+-digit existing number without truncating (no artificial ceiling)" \
  "10000" "$(run_scenario $'9999-foo.md')"

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
