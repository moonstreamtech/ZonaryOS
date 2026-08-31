#!/usr/bin/env bash
# State/checklist parsing for the self-triggering agent loop
# (.github/workflows/agent-loop.yml). Pure text transforms on an issue
# body string - no network calls, no gh/git - so this file can be sourced
# and exercised entirely offline against fixtures (see tests/run_tests.sh).
# loop.sh is the only caller that talks to GitHub; keeping that split is
# what makes the state handling here testable without burning a run.
#
# Issue body contract (see the issue template in the PR description that
# added this file):
#
#   ## Checklist
#   - [ ] <task text - this is verbatim what gets passed to aider>
#   - [ ] <task text>
#   ...
#
#   <!-- agent-state
#   turn=<n>
#   attempts_item_<i>=<n>
#   -->
#
# The checklist section is everything between a line that is exactly
# "## Checklist" and the next line starting with "#" or the
# "<!-- agent-state" marker (whichever comes first), or end of body.
# Only "- [ ] " / "- [x] " lines in that range count as items - this
# keeps checklist parsing blind to any other checkbox-shaped text a user
# might put elsewhere in the issue body (description, notes, etc).
#
# The state block is a single HTML comment (invisible in GitHub's
# rendered issue view, plain text in the raw body the API returns) holding
# line-based key=value pairs. Line-based rather than YAML/JSON so every
# function here is a few lines of awk/grep with no parser dependency.

set -euo pipefail

CHECKLIST_HEADING='## Checklist'
STATE_START='<!-- agent-state'
STATE_END='-->'

# --- checklist -----------------------------------------------------------

# Prints the raw checklist item lines ("- [ ] ..." / "- [x] ...", in
# order) found strictly inside the checklist section.
_item_lines() {
  awk -v heading="$CHECKLIST_HEADING" -v state_start="$STATE_START" '
    $0 == heading { insec=1; next }
    insec && /^#/ { insec=0 }
    insec && index($0, state_start) == 1 { insec=0 }
    insec && /^- \[[ xX]\] / { print }
  '
}

# Total number of checklist items (for the 60-item cap).
checklist_total() {
  _item_lines <<<"$1" | grep -c '^- \[' || true
}

# 1-based index of the first unchecked item, or nothing + exit 1 if none.
first_unchecked_index() {
  local idx=0 line
  while IFS= read -r line; do
    idx=$((idx + 1))
    if [[ "$line" == '- [ ] '* ]]; then
      echo "$idx"
      return 0
    fi
  done < <(_item_lines <<<"$1")
  return 1
}

# The task text of item $2 (1-based), with the "- [ ] "/"- [x] " prefix
# stripped.
item_text_at() {
  local body="$1" idx="$2" line
  line="$(_item_lines <<<"$body" | sed -n "${idx}p")"
  printf '%s' "${line:6}"
}

# Returns the full body with item $2's checkbox flipped from "[ ]" to
# "[x]" - every other line, including every other checklist item, is
# reproduced byte-for-byte. A no-op (unchanged body) if $2 is out of
# range or already checked - callers are expected to only tick an index
# they just got from first_unchecked_index in the same run.
tick_item() {
  local body="$1" target="$2"
  awk -v heading="$CHECKLIST_HEADING" -v state_start="$STATE_START" -v target="$target" '
    $0 == heading { insec=1; print; next }
    insec && /^#/ { insec=0 }
    insec && index($0, state_start) == 1 { insec=0 }
    {
      if (insec && /^- \[[ xX]\] /) {
        n++
        if (n == target) sub(/^- \[ \]/, "- [x]")
      }
      print
    }
  ' <<<"$body"
}

# Appends a new "- [ ] <text>" item at the end of the checklist section
# (before the next heading / state block / EOF). Unused by loop.sh in
# step 3a (no LLM-authored checklist items yet) - implemented and tested
# now because step 3b needs it and the format should not change once the
# loop is live.
add_item() {
  local body="$1" text="$2"
  awk -v heading="$CHECKLIST_HEADING" -v state_start="$STATE_START" -v newitem="- [ ] $text" '
    $0 == heading { insec=1; print; next }
    insec && (/^#/ || index($0, state_start) == 1) {
      if (!added) { print newitem; added=1 }
      insec=0
    }
    { print }
    END { if (insec && !added) print newitem }
  ' <<<"$body"
}

# True (exit 0) if the checklist has at least one item and none are
# unchecked.
is_finished() {
  local body="$1"
  [ "$(checklist_total "$body")" -gt 0 ] || return 1
  first_unchecked_index "$body" >/dev/null 2>&1 && return 1
  return 0
}

# --- state block -----------------------------------------------------------

# Prints only the lines strictly inside the <!-- agent-state ... --> block.
_state_lines() {
  awk -v state_start="$STATE_START" -v state_end="$STATE_END" '
    index($0, state_start) == 1 { f=1; next }
    f && $0 == state_end { f=0 }
    f
  '
}

# Generic "set (or insert) key=value inside the state block" used by
# set_turn and set_item_attempts. If no state block exists yet, one is
# appended. If the block exists but lacks the key, the key is inserted
# just before the closing "-->". If the key exists, its line is replaced
# in place - every other line in the body is untouched.
_set_state_key() {
  local body="$1" key="$2" value="$3"
  if ! grep -q "^${STATE_START}" <<<"$body"; then
    printf '%s\n\n%s\n%s=%s\n%s\n' "$body" "$STATE_START" "$key" "$value" "$STATE_END"
    return
  fi
  awk -v state_start="$STATE_START" -v state_end="$STATE_END" -v key="$key" -v value="$value" '
    index($0, state_start) == 1 { f=1; print; next }
    f && $0 == state_end {
      if (!done) print key "=" value
      f=0; print; next
    }
    f {
      if ($0 ~ "^" key "=") {
        if (!done) { print key "=" value; done=1 }
        next
      }
      print; next
    }
    { print }
  ' <<<"$body"
}

_get_state_key() {
  local body="$1" key="$2" v
  v="$(_state_lines <<<"$body" | grep -E "^${key}=" | tail -1 | cut -d= -f2-)"
  printf '%s' "${v:-}"
}

get_turn() {
  local v
  v="$(_get_state_key "$1" turn)"
  echo "${v:-0}"
}

set_turn() {
  _set_state_key "$1" turn "$2"
}

get_item_attempts() {
  local v
  v="$(_get_state_key "$1" "attempts_item_$2")"
  echo "${v:-0}"
}

set_item_attempts() {
  _set_state_key "$1" "attempts_item_$2" "$3"
}
