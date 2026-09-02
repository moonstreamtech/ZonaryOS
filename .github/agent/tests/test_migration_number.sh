#!/usr/bin/env bash
# Offline test for loop.sh's next_migration_number() and
# validate_plan_migration_numbers() - the two halves of "hand the model
# the next migration number as a stated fact instead of asking it to
# derive one" (issue #80's own plan attempts both independently invented
# migration 0007, already taken since migrations/0007_firm_invites.*.sql,
# despite discovery's own answer to this exact question feeding into the
# same prompt as "Discovery findings" text - a stated fact in prose is
# not self-enforcing, hence the second function).
#
# Real filesystem in a tempdir, not mocked - both functions' whole job
# is reading the real migrations/ directory.
#
# Usage: .github/agent/tests/test_migration_number.sh

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

assert_true() {
  local desc="$1" got="$2"
  if [ "$got" -eq 0 ]; then
    pass=$((pass + 1))
    echo "ok   - $desc"
  else
    fail=$((fail + 1))
    echo "FAIL - $desc (expected success, got failure)"
  fi
}

assert_false() {
  local desc="$1" got="$2"
  if [ "$got" -ne 0 ]; then
    pass=$((pass + 1))
    echo "ok   - $desc"
  else
    fail=$((fail + 1))
    echo "FAIL - $desc (expected failure, got success)"
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

# Populates a fresh tempdir's migrations/ per $1 (newline-separated
# filenames, empty for "no such directory"), sources loop.sh against a
# mocked `gh` (never touches the network) inside it, then runs $2.. (a
# function name plus its own arguments). Prints whatever that call
# prints and returns its exit status - callers capture both with
# `out="$(run_scenario ...)"; status=$?`.
run_scenario() {
  local files="$1"
  shift
  local tmp
  tmp="$(mktemp -d)"
  (
    cd "$tmp" || exit 1
    if [ -n "$files" ]; then
      mkdir -p migrations
      while IFS= read -r f; do
        [ -n "$f" ] && touch "migrations/$f"
      done <<<"$files"
    fi
    export GITHUB_REPOSITORY="acme/example"
    export GITHUB_RUN_ID="999999"
    gh() { return 0; }
    # shellcheck source=../loop.sh
    source "$LOOP_SH"
    trap - EXIT # loop.sh installs a print_resolved_state EXIT trap on
                # sourcing; not relevant here and would otherwise
                # contaminate captured stdout.
    "$@"
  )
  local status=$?
  rm -rf "$tmp"
  return $status
}

# --- next_migration_number -------------------------------------------------

assert_eq "no migrations/ directory at all: starts at 0001" \
  "0001" "$(run_scenario "" next_migration_number)"

assert_eq "empty migrations/ directory: starts at 0001" \
  "0001" "$(run_scenario $'' next_migration_number)"

assert_eq "one existing migration (0001_foo.up.sql): next is 0002" \
  "0002" "$(run_scenario $'0001_foo.up.sql' next_migration_number)"

assert_eq "a number's .up.sql and .down.sql pair does not get double-counted or confused: next is 0002" \
  "0002" "$(run_scenario $'0001_foo.up.sql\n0001_foo.down.sql' next_migration_number)"

assert_eq "several existing migrations, out of order on disk: picks the highest" \
  "0047" "$(run_scenario $'0007_firm_invites.up.sql\n0007_firm_invites.down.sql\n0046_fix_request_metrics_partition_grant.up.sql\n0046_fix_request_metrics_partition_grant.down.sql\n0023_api_keys_webhooks.up.sql' next_migration_number)"

assert_eq "zero-pads to 4 digits past 9" \
  "0010" "$(run_scenario $'0009_foo.up.sql' next_migration_number)"

assert_eq "does not get confused by a non-numeric-prefixed file" \
  "0005" "$(run_scenario $'embed.go\n0004_foo.up.sql' next_migration_number)"

assert_eq "handles a 4+-digit existing number without truncating" \
  "10000" "$(run_scenario $'9999_foo.up.sql' next_migration_number)"

# --- validate_plan_migration_numbers ---------------------------------------

existing=$'0007_firm_invites.up.sql\n0007_firm_invites.down.sql'

out="$(run_scenario "$existing" validate_plan_migration_numbers 'None')"
assert_true "a plan needing no migration ('None') passes trivially" $?

out="$(run_scenario "$existing" validate_plan_migration_numbers '- `migrations/0047_add_widget.up.sql`
- `migrations/0047_add_widget.down.sql`')"
assert_true "a genuinely free migration number passes" $?

out="$(run_scenario "$existing" validate_plan_migration_numbers '- `migrations/0007_add_widget.up.sql`
- `migrations/0007_add_widget.down.sql`')"
status=$?
assert_false "issue #80's own collision (reusing an already-taken number) is rejected" "$status"
assert_contains "... with a message naming the taken number" "$out" "0007"
assert_contains "... explicitly, not just a generic failure" "$out" "already taken"

out="$(run_scenario "$existing" validate_plan_migration_numbers '- `migrations/0047_add_widget.up.sql`
- `migrations/0007_add_other.up.sql`')"
status=$?
assert_false "rejects if ANY of several referenced numbers collides, not just the first" "$status"

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
