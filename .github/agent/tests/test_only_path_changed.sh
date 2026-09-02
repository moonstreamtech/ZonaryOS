#!/usr/bin/env bash
# Offline test for loop.sh's only_path_changed(): does it correctly
# detect "git status shows exactly this one path changed, nothing else"
# against REAL git output? Not mocked - this function's entire job is
# parsing `git status --porcelain=v1 --untracked-files=all -z`, so a
# fake `git` would test nothing about the thing that actually broke.
#
# Covers the exact regression from issue #80's own first live run
# (turns 2 and 3): a brand-new untracked directory collapses to one
# porcelain entry for the directory itself, discarding two genuinely
# well-formed plans because docs/plans/ didn't exist yet - plus the two
# other failure modes the old `awk '{print $NF}'` version had (a path
# containing a space, and a rename), per that incident's own follow-up
# review.
#
# Usage: .github/agent/tests/test_only_path_changed.sh

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOOP_SH="$HERE/../loop.sh"

pass=0
fail=0

assert_true() {
  local desc="$1" got="$2"
  if [ "$got" -eq 0 ]; then
    pass=$((pass + 1))
    echo "ok   - $desc"
  else
    fail=$((fail + 1))
    echo "FAIL - $desc (expected only_path_changed to succeed, it did not)"
  fi
}

assert_false() {
  local desc="$1" got="$2"
  if [ "$got" -ne 0 ]; then
    pass=$((pass + 1))
    echo "ok   - $desc"
  else
    fail=$((fail + 1))
    echo "FAIL - $desc (expected only_path_changed to fail, it succeeded)"
  fi
}

assert_eq() {
  local desc="$1" expected="$2" actual="$3"
  if [ "$expected" = "$actual" ]; then
    pass=$((pass + 1))
    echo "ok   - $desc"
  else
    fail=$((fail + 1))
    echo "FAIL - $desc"
    echo "       expected: $(printf '%q' "$expected")"
    echo "       actual:   $(printf '%q' "$actual")"
  fi
}

# Runs a real `git init`-ed tempdir through $1 (a bash snippet that
# makes the working-tree changes the scenario is testing), then calls
# only_path_changed against $2 (the expected single path). Echoes
# only_path_changed's own printed summary and returns its exit status,
# so a caller can assert on both.
run_scenario() {
  local setup="$1" expected="$2"
  local tmp
  tmp="$(mktemp -d)"
  (
    cd "$tmp" || exit 1
    git init -q
    git config user.email test@example.com
    git config user.name test
    # A committed baseline so the scenario's own changes are real git
    # status entries (untracked/modified/renamed against something),
    # not "nothing to compare against yet".
    echo baseline >baseline.txt
    git add baseline.txt
    git commit -q -m baseline

    eval "$setup"

    export GITHUB_REPOSITORY="acme/example"
    export GITHUB_RUN_ID="999999"
    gh() { return 0; }
    # shellcheck source=../loop.sh
    source "$LOOP_SH"
    trap - EXIT # loop.sh installs a print_resolved_state EXIT trap on
                # sourcing; not relevant to this test and would
                # otherwise contaminate only_path_changed's captured
                # stdout.

    only_path_changed "$expected"
  )
  local status=$?
  rm -rf "$tmp"
  return $status
}

# --- the actual #80 regression: a brand-new untracked directory --------

out="$(run_scenario 'mkdir -p docs/plans; printf "## Steps\n- [ ] x\n" >docs/plans/0001-foo.md' \
  'docs/plans/0001-foo.md')"
status=$?
assert_true "a new file in a brand-new directory: matches the file, not the collapsed directory entry (issue #80's own regression)" "$status"
assert_eq "... and the summary is the file path itself, not the collapsed directory" \
  "docs/plans/0001-foo.md" "$out"

# --- a path containing a space ------------------------------------------

run_scenario 'mkdir -p docs/plans; printf "content" >"docs/plans/my plan.md"' \
  'docs/plans/my plan.md' >/dev/null
assert_true "a path containing a space is matched exactly, not mangled by whitespace splitting" $?

# --- a rename -------------------------------------------------------------

run_scenario 'mkdir -p docs/plans; printf x >docs/plans/old-name.md; git add docs/plans/old-name.md; git commit -q -m first; git mv docs/plans/old-name.md docs/plans/new-name.md' \
  'docs/plans/new-name.md' >/dev/null
assert_true "a rename is matched against its destination path, not misparsed as 'old -> new'" $?

# --- negative: more than the expected path changed ------------------------

run_scenario 'mkdir -p docs/plans; printf a >docs/plans/0001-foo.md; printf b >unrelated.go' \
  'docs/plans/0001-foo.md' >/dev/null
assert_false "rejects when aider touched something beyond the expected path too" $?

# --- negative: nothing changed at all --------------------------------------

run_scenario ':' 'docs/plans/0001-foo.md' >/dev/null
assert_false "rejects when nothing changed at all" $?

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
