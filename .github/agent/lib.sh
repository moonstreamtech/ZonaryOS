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
PLAN_DIR='docs/plans'

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

# --- phase (step 3b-1) ------------------------------------------------------
#
# phase= lives in the same state block as turn=, values: discovery, plan,
# awaiting-plan, implement. Back-compat default when the key is absent
# (an issue created before phases existed, or a fresh 3b issue on its
# very first read before any phase has ever been written): "implement"
# if the body already has checklist items (a pre-existing 3a issue - keep
# it running exactly as it always has, untouched), otherwise "discovery"
# (a new 3b issue with nothing but a feature description yet).
get_phase() {
  local v
  v="$(_get_state_key "$1" phase)"
  if [ -n "$v" ]; then
    echo "$v"
    return
  fi
  if [ "$(checklist_total "$1")" -gt 0 ]; then
    echo "implement"
  else
    echo "discovery"
  fi
}

set_phase() {
  _set_state_key "$1" phase "$2"
}

get_phase_attempts() {
  local v
  v="$(_get_state_key "$1" "attempts_phase_$2")"
  echo "${v:-0}"
}

set_phase_attempts() {
  _set_state_key "$1" "attempts_phase_$2" "$3"
}

# The plan file path claimed for this issue, once the plan phase has
# chosen one (see plan_filename's comment on why it's persisted rather
# than recomputed). Empty if none has been claimed yet.
get_plan_file() {
  _get_state_key "$1" plan_file
}

set_plan_file() {
  _set_state_key "$1" plan_file "$2"
}

# --- plan file parsing (step 3b-1) ------------------------------------------

# Extracts the "## Steps" section's checkbox lines ("- [ ] ..." /
# "- [x] ...") from a plan markdown file's content, verbatim and in
# order, preserving whatever checked state each line already carries -
# so if a human hand-edits the plan before merging it (e.g. pre-checking
# a step they already did by hand, rewording a step, deleting one),
# whatever lands on main is exactly what gets parsed, not what the agent
# originally wrote. Same section-boundary convention as the issue body's
# own "## Checklist" section (_item_lines above): opens on a line that is
# exactly "## Steps", closes on the next line starting with "#".
#
# Fails (empty output, exit 1) on a malformed plan: no "## Steps" heading
# at all, or a "## Steps" section with zero checkbox lines in it. Callers
# must treat that as a hard stop, not an empty-but-valid checklist - see
# loop.sh's awaiting-plan phase.
plan_steps() {
  local lines
  lines="$(awk '
    $0 == "## Steps" { insec=1; next }
    insec && /^#/ { insec=0 }
    insec && /^- \[[ xX]\] / { print }
  ' <<<"$1")"
  [ -n "$lines" ] || return 1
  printf '%s\n' "$lines"
}

# Populates the issue body's "## Checklist" section with $2 (a
# newline-separated block of raw "- [ ] ..."/"- [x] ..." lines, e.g. from
# plan_steps), creating the heading if the body doesn't have one yet.
# Only ever called once, on the awaiting-plan -> implement transition,
# against a body whose checklist section is empty or absent - if a
# "## Checklist" heading somehow already has items under it, they are
# replaced wholesale, not merged, since re-running this transition against
# an already-populated checklist should not happen by construction (the
# phase moves to "implement" in the same write that populates the
# checklist - see set_issue_body's atomicity in loop.sh).
set_checklist_from_lines() {
  local body="$1" new_lines="$2"
  if grep -qx -- "$CHECKLIST_HEADING" <<<"$body"; then
    awk -v heading="$CHECKLIST_HEADING" -v state_start="$STATE_START" -v newlines="$new_lines" '
      BEGIN { n = split(newlines, arr, "\n") }
      $0 == heading {
        print
        for (i = 1; i <= n; i++) print arr[i]
        insec = 1
        next
      }
      insec && /^#/ { insec = 0 }
      insec && index($0, state_start) == 1 { insec = 0 }
      insec && /^- \[[ xX]\] / { next }
      { print }
    ' <<<"$body"
  else
    awk -v heading="$CHECKLIST_HEADING" -v state_start="$STATE_START" -v newlines="$new_lines" '
      BEGIN { n = split(newlines, arr, "\n"); added = 0 }
      index($0, state_start) == 1 {
        if (!added) {
          print heading
          for (i = 1; i <= n; i++) print arr[i]
          print ""
          added = 1
        }
        print
        next
      }
      { print }
      END {
        if (!added) {
          print heading
          for (i = 1; i <= n; i++) print arr[i]
        }
      }
    ' <<<"$body"
  fi
}

# Lowercases, replaces every run of non-alphanumeric characters with a
# single hyphen, and trims leading/trailing hyphens - the standard
# filename-slug transform. Capped at 60 characters (after trimming any
# hyphen the cap itself introduced) so a long issue title can't produce
# an unreasonable filename.
slugify() {
  local s
  s="$(tr '[:upper:]' '[:lower:]' <<<"$1")"
  s="$(sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//' <<<"$s")"
  s="${s:0:60}"
  sed -E 's/-+$//' <<<"$s"
}

# The plan file path for a given (already zero-padded, e.g. "0001")
# number and issue title - docs/plans/<number>-<slug>.md. Pure string
# formatting; the number itself comes from scanning the real docs/plans/
# directory (loop.sh's next_plan_number, not offline-testable since it
# touches the filesystem) and is persisted in the state block's
# plan_file= key the moment it's chosen, precisely so a crashed-and-
# retried plan turn reuses the same reserved filename instead of
# computing a new one against a directory that may have changed.
plan_filename() {
  printf '%s/%s-%s.md' "$PLAN_DIR" "$1" "$(slugify "$2")"
}

# --- aider chat-history parsing (step 3b-1) ---------------------------------

# Extracts the assistant's reply to the LAST user message from an aider
# chat-history-file's content (see aider/io.py: user_input() writes each
# line of the prompt prefixed "#### ", ai_output() writes the reply raw,
# directly after, with no prefix - confirmed against aider 0.86.2's own
# source, not guessed). Used to post aider's discovery/plan answer as an
# issue comment without scraping --verbose/LITELLM_LOG debug noise out of
# the job log. Each loop.sh turn writes to a fresh history file, so there
# is exactly one prompt/reply pair to extract - this takes the text after
# the LAST "#### "-prefixed line for robustness if that ever changes.
extract_last_reply() {
  awk '
    /^#### / { last = NR; next }
    { lines[NR] = $0 }
    END { for (i = last + 1; i <= NR; i++) print lines[i] }
  ' <<<"$1" | sed -e '/./,$!d' -e ':a;/^$/{$d;N;ba}'
}
