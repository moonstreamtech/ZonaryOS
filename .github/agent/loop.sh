#!/usr/bin/env bash
# Step 3b-1: phase machine, discovery, and plan generation - orchestration
# only. Builds on step 3a (the self-triggering per-item implement loop,
# unchanged below as run_implement_phase - brakes proven live on issue
# #76's verify-after-write). All checklist/state/plan-parsing logic lives
# in lib.sh, which is unit-tested offline (.github/agent/tests/
# run_tests.sh) - this file is the thin, mostly-untestable-without-a-
# real-run part: gh/git/aider calls. Some of THIS file's own control flow
# IS covered offline against a mocked `gh` (never touches the network):
# gh_or_block/block_issue's failure handling (tests/test_gh_or_block.sh),
# set_issue_body's verify-after-write check (tests/
# test_state_verification.sh), and guard_run_rate's threshold logic
# (tests/test_guard_run_rate.sh) - see each file's own header for exactly
# what it does and does not prove.
#
# Deliberately NOT here (step 3b-2): web research, and a multi-model
# pool. Same reasoning as every step before this one - one new variable
# per build, so a break is attributable to a single thing that changed.
#
# TODO(step 3b-2): --yes-always below (both in run_implement_phase and
# run_plan_phase) is what let a swapped-input smoke-test run (see
# agent-smoke.yml's history) create files from bogus arguments without
# asking. As of 3b-1, checklist/plan text still only reaches this script
# via whoever can apply agent:go (a repo collaborator with label
# permissions) or merge a plan PR (review-gated) - not yet arbitrary
# issue-comment text. Step 3b-2's web research and any future
# issue-comment-sourced input would change that trust boundary; --yes-
# always needs a real guard before that lands, not just this comment.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib.sh
source "$HERE/lib.sh"

REPO="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must be set}"
WORKFLOW_FILE="agent-loop.yml"
MAX_TURNS=80
MAX_ITEMS=60
MAX_ITEM_ATTEMPTS=3
MAX_PHASE_ATTEMPTS=2
MODEL="gemini/gemini-3.5-flash-lite"
# Discovery and plan use the strongest model on this account's free tier
# (5 RPM / 20 RPD - the ceiling this note exists to explain: a couple of
# plans a day, no more, until step 3b-2's model pool raises it). The
# implement phase stays on Flash Lite (15 RPM / 500 RPD) since it runs
# once per checklist item, far more often than discovery/plan run per
# issue.
DISCOVERY_PLAN_MODEL="gemini/gemini-3.7-flash"
EDIT_FORMAT="diff-fenced"
MAP_TOKENS=4096
AIDER_LOG="/tmp/agent-loop-aider.log"
CHAT_HISTORY_FILE="/tmp/agent-loop-chat-history.md"

LABEL_GO="agent:go"
LABEL_BLOCKED="agent:blocked"
LABEL_DONE="agent:done"
LABEL_PAUSED="agent:paused"

# --- diagnostics: always print resolved state near the end of the log ----
# Same reasoning as agent-smoke.yml's "Print resolved dispatch inputs"
# step: get_job_logs-style log tools clamp to a job's last ~N lines, and
# a self-triggering loop is exactly the kind of thing someone will need
# to diagnose after the fact. A trap (not a final echo) so this still
# prints on an unexpected early exit, not just the happy path.
ISSUE_NUMBER=""
TURN=""
PHASE=""
ITEM_INDEX=""
ITEM_TOTAL=""
ITEM_ATTEMPTS=""
ACTION="unknown"
RETRIGGER="false"
LAST_GH_CMD=""

print_resolved_state() {
  echo "RESOLVED STATE: issue=#${ISSUE_NUMBER:-none} phase=${PHASE:-?} turn=${TURN:-?} item=${ITEM_INDEX:-?}/${ITEM_TOTAL:-?} attempts=${ITEM_ATTEMPTS:-?} action=${ACTION} retrigger=${RETRIGGER} last_gh_cmd=[${LAST_GH_CMD:-none}]"
}
trap print_resolved_state EXIT

# --- gh/git helpers --------------------------------------------------------

find_active_issue() {
  local issues count
  issues="$(gh issue list --repo "$REPO" --label "$LABEL_GO" --state open --json number -q '.[].number')"
  count="$(grep -c . <<<"$issues" || true)"
  if [ -z "$issues" ] || [ "$count" -eq 0 ]; then
    return 1
  fi
  if [ "$count" -gt 1 ]; then
    echo "Multiple issues carry $LABEL_GO (#$(tr '\n' ' ' <<<"$issues")) - refusing to guess which one to work on. Fix this manually (only one issue should carry $LABEL_GO at a time)." >&2
    return 1
  fi
  echo "$issues"
}

issue_body() { gh issue view "$1" --repo "$REPO" --json body -q .body; }
issue_labels() { gh issue view "$1" --repo "$REPO" --json labels -q '.labels[].name'; }
issue_title() { gh issue view "$1" --repo "$REPO" --json title -q .title; }

# The most recent discovery-findings comment on the issue, or empty if
# none exists yet. Matched by the fixed emoji+text prefix run_discovery_
# phase always posts with - not by author, since the same GITHUB_TOKEN
# identity posts every comment this script makes.
discovery_comment() {
  gh issue view "$1" --repo "$REPO" --json comments \
    -q '[.comments[] | select(.body | startswith("🔍 Discovery findings"))] | if length > 0 then .[-1].body else "" end'
}

# Best-effort: try to move the issue to agent:blocked and leave a comment
# explaining why. Deliberately makes its own raw `gh` calls rather than
# going through gh_or_block below - it exists specifically to be what
# gh_or_block falls back to when a gh mutation fails, and if it went
# through gh_or_block too, a failure here would call itself again
# (infinite recursion) the moment the underlying problem is something
# systemic like a revoked token, where every gh call fails the same way.
# Failures here are logged loudly and swallowed, never masking whatever
# the caller already reported as the real error.
block_issue() {
  local issue="$1" reason="$2"
  if ! gh issue edit "$issue" --repo "$REPO" --remove-label "$LABEL_GO" --add-label "$LABEL_BLOCKED" >/dev/null 2>&1; then
    echo "block_issue: could not swap labels on #$issue (agent:go -> agent:blocked). Continuing to at least try the comment." >&2
  fi
  if ! gh issue comment "$issue" --repo "$REPO" --body "🛑 Blocked: $reason" >/dev/null 2>&1; then
    echo "block_issue: could not post the blocking comment on #$issue either. This issue may be left in an undiagnosable agent:go state - check it manually." >&2
  fi
  ACTION="blocked"
  RETRIGGER="false"
}

# Every mutating gh call this script makes (pr create, pr ready, issue
# edit, issue comment, the self-retrigger workflow run) goes through
# here. Run 33366155853 showed why: gh pr create failed on a repo-policy
# rejection, that failure was an uncaught `set -e` death, and because it
# happened before any state got persisted, the turn counter never
# advanced and the cron would have retried the identical failure every
# 30 minutes indefinitely. Routing every mutation through one place
# means every one of them now degrades the same way: log the real
# failure, best-effort block_issue (which DOES persist state - swapping
# agent:go for agent:blocked is what makes the next run's kill-switch
# check actually stop the chain), then exit non-zero reporting the
# ORIGINAL failure - never block_issue's own, even if block_issue's
# calls fail too (see its comment on why it can't recurse back here).
gh_or_block() {
  LAST_GH_CMD="gh $*"
  local output status=0
  # `|| status=$?` is load-bearing under `set -e`: a bare
  # `output="$(gh ... )"` assignment with no fallback would let a
  # failing gh call kill this function (and the whole script) via set -e
  # BEFORE the status/output check below ever ran - the exact class of
  # bug this function exists to stop happening anywhere else.
  output="$(gh "$@" 2>&1)" || status=$?
  if [ "$status" -eq 0 ]; then
    if [ -n "$output" ]; then
      printf '%s\n' "$output"
    fi
    return 0
  fi

  echo "gh command failed (exit $status): $LAST_GH_CMD" >&2
  echo "$output" >&2

  if [ -n "$ISSUE_NUMBER" ]; then
    block_issue "$ISSUE_NUMBER" "an automated gh command failed: \`$LAST_GH_CMD\`. See this turn's job log for the full error."
  else
    echo "No issue number resolved yet - nothing to block." >&2
  fi

  ACTION="gh_command_failed"
  RETRIGGER="false"
  exit 1
}

# Writes the issue body, then reads it back and verifies the turn we
# just wrote is actually there. Every caller passes a body that already
# went through set_turn, so get_turn(body) is exactly the value this
# write is supposed to make durable.
#
# Why this exists: on issue #73's validation chain, every single run's
# RESOLVED STATE showed turn=1, and the issue's final closed body had no
# <!-- agent-state --> block at all, despite the checklist itself
# persisting correctly across all 8 runs and despite tick_item/set_turn
# composing correctly against local fixtures (including CRLF bodies).
# The bug is somewhere in the real gh issue edit / gh issue view round
# trip, not in lib.sh's parsing. What matters operationally: MAX_TURNS
# and the per-item/per-phase attempt caps all read state from a freshly
# re-fetched body every run, so if a write silently doesn't take, every
# brake built on top of it fails at once, silently. Catch it here,
# immediately after every write, and stop hard.
set_issue_body() {
  local issue="$1" body="$2"
  local expected_turn
  expected_turn="$(get_turn "$body")"

  gh_or_block issue edit "$issue" --repo "$REPO" --body "$body" >/dev/null

  local roundtrip actual_turn
  roundtrip="$(issue_body "$issue")"
  actual_turn="$(get_turn "$roundtrip")"

  if [ "$actual_turn" != "$expected_turn" ]; then
    echo "State verification failed on #$issue: wrote turn=$expected_turn but reading the issue back shows turn=$actual_turn (or no <!-- agent-state --> block at all). Every brake depends on this write surviving, so this is a hard stop, not a retry." >&2
    block_issue "$issue" "state verification failed after writing the issue body: wrote turn=$expected_turn but reading the issue back shows turn=$actual_turn. The write may not be persisting - do not re-add agent:go until this is understood; re-adding it now would resume with brakes that cannot fire."
    ACTION="state_verification_failed"
    RETRIGGER="false"
    exit 1
  fi
}

post_comment() {
  local issue="$1" text="$2"
  gh_or_block issue comment "$issue" --repo "$REPO" --body "$text"
}

# Prints the PR number for $1's head branch, or nothing if none exists yet
# (gh's -q on an empty match set prints the literal string "null", which
# callers must check for explicitly).
pr_number_for_branch() {
  local n
  n="$(gh pr list --repo "$REPO" --head "$1" --json number -q '.[0].number')"
  [ "$n" = "null" ] && return
  printf '%s' "$n"
}

# Idempotent: safe to call on every turn, not just the first. Creates the
# branch if it doesn't exist on the remote yet, otherwise fetches and
# switches to what's already there. This is what lets a turn that died
# between "branch pushed" and "PR created" recover cleanly on its next
# attempt (self-retrigger or the cron safety net) instead of getting
# stuck with a branch and no PR forever - see ensure_pr for the other
# half of that.
ensure_branch() {
  local branch="$1"
  if git fetch origin "$branch" 2>/dev/null; then
    git switch "$branch"
  else
    git switch -c "$branch"
    git commit --allow-empty -m "agent: start work on #$ISSUE_NUMBER"
    git push -u origin "$branch"
  fi
}

# Idempotent: safe to call on every turn. Creates the draft PR only if
# the branch doesn't already have one.
ensure_pr() {
  local issue="$1" branch="$2"
  if [ -n "$(pr_number_for_branch "$branch")" ]; then
    return
  fi
  gh_or_block pr create --repo "$REPO" --draft --base main --head "$branch" \
    --title "Agent: issue #$issue" \
    --body "Closes #$issue

Opened and driven by .github/workflows/agent-loop.yml. One checklist item per commit; see the issue for progress and the commit-by-commit history for what changed and why. This PR is never merged by the agent - it always stops ready for human review."
}

# Same idempotency contract as ensure_branch, for the SEPARATE plan
# branch (agent/plan-<n>, never agent/issue-<n> - merging a plan must
# never merge code, so these branches/PRs are always distinct). No
# empty-commit-then-push here: unlike the issue branch, this one isn't
# meant to have a PR until the plan file exists and validates, so a
# failed plan attempt just re-creates the same empty local branch fresh
# next turn (a new VM each time) rather than pushing anything - only a
# successful attempt (run_plan_phase's success path) commits and pushes.
ensure_plan_branch() {
  local branch="$1"
  if git fetch origin "$branch" 2>/dev/null; then
    git switch "$branch"
  else
    git switch -c "$branch"
  fi
}

# Idempotent like ensure_pr. NOT a draft - reviewing and merging a plan
# PR is exactly the human-in-the-loop step this phase exists to produce.
ensure_plan_pr() {
  local issue="$1" branch="$2" plan_file="$3"
  if [ -n "$(pr_number_for_branch "$branch")" ]; then
    return
  fi
  gh_or_block pr create --repo "$REPO" --base main --head "$branch" \
    --title "Plan: issue #$issue" \
    --body "Plan for #$issue.

Review \`$plan_file\`'s \`## Steps\` section - that becomes the implementation checklist the moment this PR is merged. Edit it freely before merging (reword a step, add or remove one, pre-check a step you already did by hand) - whatever is on \`main\` when this merges is exactly what gets parsed, not what the agent originally wrote. Merging this PR does not touch any code; it only starts the implementation phase, which opens its own separate draft PR."
}

# Scans docs/plans/ for the highest existing NNNN- prefix and returns the
# next one, zero-padded to 4 digits (matching this repo's migrations/
# NNNN_*.sql convention). Touches the filesystem, so - like
# worktree_has_changes - this lives here rather than in lib.sh, which is
# offline-testable-only by staying filesystem-free.
next_plan_number() {
  local max=0 n
  if [ -d "$PLAN_DIR" ]; then
    while IFS= read -r f; do
      n="$(basename "$f" | grep -oE '^[0-9]+' || true)"
      if [ -n "$n" ] && [ "$((10#$n))" -gt "$max" ]; then
        max=$((10#$n))
      fi
    done < <(find "$PLAN_DIR" -maxdepth 1 -type f -name '*.md')
  fi
  printf '%04d' $((max + 1))
}

# --- aider --------------------------------------------------------------

# Shared discovery/plan quota+reply plumbing: runs $2.. as the aider
# command line, tees to AIDER_LOG, and echoes any rate-limit/quota lines
# found afterward. No LITELLM_LOG=DEBUG or --verbose here (unlike
# run_aider_on_item) - aider's own exception handling prints the raw
# provider error body (including RESOURCE_EXHAUSTED/429) regardless of
# either, confirmed against run 33313795551's log, which showed that text
# before this workflow ever set LITELLM_LOG=DEBUG - so aider_hit_quota_
# wall still works, and dropping both keeps the log clean enough to
# extract a reply from CHAT_HISTORY_FILE without scraping debug noise.
_run_aider_readonly_or_editing() {
  rm -f "$CHAT_HISTORY_FILE"
  set +e
  "$@" | tee "$AIDER_LOG"
  set -e
  echo "--- rate-limit/quota lines surfaced by this call, if any ---"
  grep -iE 'ratelimit|rate-limit|retry-after|quota|RESOURCE_EXHAUSTED' "$AIDER_LOG" || echo "(none)"
}

run_aider_on_item() {
  local text="$1"
  printf '%s' "$text" >/tmp/agent-loop-task.txt
  set +e
  LITELLM_LOG=DEBUG aider \
    --model "$MODEL" \
    --edit-format "$EDIT_FORMAT" \
    --model-settings-file .github/aider-model-settings.yml \
    --message-file /tmp/agent-loop-task.txt \
    --no-auto-commits \
    --yes-always \
    --no-check-update \
    --no-analytics \
    --no-gitignore \
    --no-pretty \
    --no-stream \
    --map-tokens "$MAP_TOKENS" \
    --read CLAUDE.md \
    --read docs/RULES.md \
    --verbose \
    | tee "$AIDER_LOG"
  set -e
  echo "--- rate-limit/quota lines surfaced by this call, if any ---"
  grep -iE 'ratelimit|rate-limit|retry-after|quota|RESOURCE_EXHAUSTED' "$AIDER_LOG" || echo "(none)"
}

run_aider_discovery() {
  local prompt="$1"
  printf '%s' "$prompt" >/tmp/agent-loop-task.txt
  _run_aider_readonly_or_editing aider \
    --model "$DISCOVERY_PLAN_MODEL" \
    --chat-mode ask \
    --read CLAUDE.md \
    --chat-history-file "$CHAT_HISTORY_FILE" \
    --message-file /tmp/agent-loop-task.txt \
    --no-check-update \
    --no-analytics \
    --no-gitignore \
    --no-pretty \
    --no-stream \
    --map-tokens "$MAP_TOKENS"
}

run_aider_plan() {
  local prompt="$1" plan_file="$2"
  printf '%s' "$prompt" >/tmp/agent-loop-task.txt
  _run_aider_readonly_or_editing aider \
    --model "$DISCOVERY_PLAN_MODEL" \
    --edit-format "$EDIT_FORMAT" \
    --model-settings-file .github/aider-model-settings.yml \
    --chat-history-file "$CHAT_HISTORY_FILE" \
    --message-file /tmp/agent-loop-task.txt \
    --no-auto-commits \
    --yes-always \
    --no-check-update \
    --no-analytics \
    --no-gitignore \
    --no-pretty \
    --no-stream \
    --map-tokens "$MAP_TOKENS" \
    "$plan_file"
}

aider_hit_quota_wall() {
  grep -qiE 'RESOURCE_EXHAUSTED|"code": *429' "$AIDER_LOG" 2>/dev/null
}

run_fast_gate() {
  go build ./... && go vet ./... && go test ./... && python3 scripts/license_headers.py --check
}

# `git diff --quiet` only compares tracked files against the index, so a
# turn whose entire contribution is a brand-new file (a migration, a new
# package) reads as "no changes" and the loop would record a false
# failed attempt. `git status --porcelain` covers untracked files too.
# The .aider* entry in .gitignore is what keeps this a truthful signal -
# without it, aider's own scratch files (.aider.chat.history.md etc.,
# left behind because aider runs with --no-gitignore) would always show
# up here, making every turn look like it produced a change even when it
# didn't.
worktree_has_changes() {
  [ -n "$(git status --porcelain)" ]
}

# Independent of every other brake on purpose: MAX_TURNS and the
# per-item/per-phase attempt caps all read state this loop wrote to the
# issue body on a previous run, and issue #73's validation chain showed
# that write can silently not persist - which means every brake built on
# top of it can fail at once, without an error anywhere. This one reads
# GitHub Actions' own run history instead, which the loop cannot corrupt
# by writing bad state, so it still catches a runaway chain even if state
# persistence is broken again in some new way. Deliberately crude (a flat
# count in a time window, no issue or phase awareness) and deliberately
# not routed through gh_or_block/block_issue - sharing no machinery with
# the other brakes is the whole point. Covers every phase, since it's the
# very first thing main() does, before phase is even read.
guard_run_rate() {
  local window_minutes=60 max_runs=30
  local cutoff count
  cutoff="$(date -u -d "-${window_minutes} minutes" +%Y-%m-%dT%H:%M:%SZ)"
  count="$(gh run list --repo "$REPO" --workflow "$WORKFLOW_FILE" --limit 100 --json createdAt \
    -q "[.[] | select(.createdAt >= \"$cutoff\")] | length")"
  if [ "$count" -ge "$max_runs" ]; then
    echo "$WORKFLOW_FILE has run $count times in the last $window_minutes minutes (threshold: $max_runs). Refusing to proceed. This check does not depend on issue state, so it still works even if turn/attempt persistence is broken - see set_issue_body's comment for why that matters. If this is legitimate (a long chain making real progress), raise max_runs; if it's a runaway, the affected issue's agent:go label needs to come off by hand." >&2
    ACTION="run_rate_exceeded"
    RETRIGGER="false"
    exit 0
  fi
}

# --- phases (step 3b-1) ------------------------------------------------

# Read-only reconnaissance, one turn. --chat-mode ask makes no edits by
# design - confirmed against aider 0.86.2's own source (AskCoder never
# overrides Coder.get_edits/apply_edits, so it inherits the base no-op
# that returns [] / does nothing) and empirically (a local --exit run
# showed edit_format: ask parses cleanly). Asserted again below anyway:
# a silent worktree change here would corrupt every phase after this one,
# which never expect discovery to have touched anything.
run_discovery_phase() {
  local attempts
  attempts="$(get_phase_attempts "$body" discovery)"
  attempts=$((attempts + 1))
  echo "Turn $TURN: issue #$ISSUE_NUMBER, phase discovery, attempt $attempts/$MAX_PHASE_ATTEMPTS"

  local prompt
  prompt="For the feature described below, answer concretely - name real files, real functions, real table names. Do NOT propose code changes; this is a reconnaissance pass only.

- Which existing packages are involved?
- Which existing patterns and helpers must be reused?
- Which tables and migrations are touched?
- What is the next migration number (highest existing migrations/NNNN_*.sql, plus one)?
- Which of CLAUDE.md's never-violate rules apply?

Feature (from the issue):
$body"

  run_aider_discovery "$prompt"

  if worktree_has_changes; then
    block_issue "$ISSUE_NUMBER" "discovery (--chat-mode ask, which makes no edits by design) left uncommitted changes in the worktree - this should be impossible. Investigate manually before re-adding $LABEL_GO."
    exit 1
  fi

  if aider_hit_quota_wall; then
    echo "Gemini returned a persistent quota error during discovery. Stopping without retrigger; the schedule trigger will pick this issue back up."
    body="$(set_turn "$body" "$TURN")"
    set_issue_body "$ISSUE_NUMBER" "$body"
    post_comment "$ISSUE_NUMBER" "⏸️ Turn $TURN: Gemini's free-tier quota is exhausted during discovery. Not counted as a failed attempt. The scheduled run will retry automatically."
    ACTION="budget_exhausted"
    RETRIGGER="false"
    exit 0
  fi

  local reply=""
  if [ -f "$CHAT_HISTORY_FILE" ]; then
    reply="$(extract_last_reply "$(cat "$CHAT_HISTORY_FILE")")"
  fi

  if [ -z "$reply" ]; then
    echo "Discovery produced no answer and no quota error was logged - treating as a failed attempt."
    body="$(set_phase_attempts "$body" discovery "$attempts")"
    body="$(set_turn "$body" "$TURN")"
    set_issue_body "$ISSUE_NUMBER" "$body"
    if [ "$attempts" -ge "$MAX_PHASE_ATTEMPTS" ]; then
      block_issue "$ISSUE_NUMBER" "discovery failed $attempts times: no answer was produced. See turn $TURN's job log."
      exit 0
    fi
    post_comment "$ISSUE_NUMBER" "⚠️ Turn $TURN: discovery attempt $attempts/$MAX_PHASE_ATTEMPTS produced no answer. Will retry."
    ACTION="failed_phase_retry"
    RETRIGGER="true"
    retrigger_self
    exit 0
  fi

  post_comment "$ISSUE_NUMBER" "🔍 Discovery findings (turn $TURN):

$reply"
  body="$(set_phase "$body" plan)"
  body="$(set_turn "$body" "$TURN")"
  set_issue_body "$ISSUE_NUMBER" "$body"
  ACTION="discovery_complete"
  RETRIGGER="true"
  retrigger_self
}

# Writes docs/plans/NNNN-<slug>.md on its own branch (agent/plan-<n>,
# never agent/issue-<n> - see ensure_plan_branch) and opens a normal PR
# for it, then stops: no re-trigger on success, the chain waits for
# Kaan's merge (see run_awaiting_plan_phase / the docs/plans/** push
# trigger in agent-loop.yml for how it resumes).
run_plan_phase() {
  local attempts
  attempts="$(get_phase_attempts "$body" plan)"
  attempts=$((attempts + 1))
  echo "Turn $TURN: issue #$ISSUE_NUMBER, phase plan, attempt $attempts/$MAX_PHASE_ATTEMPTS"

  local plan_branch="agent/plan-$ISSUE_NUMBER"
  ensure_plan_branch "$plan_branch"

  local plan_file
  plan_file="$(get_plan_file "$body")"
  if [ -z "$plan_file" ]; then
    local title number
    title="$(issue_title "$ISSUE_NUMBER")"
    number="$(next_plan_number)"
    plan_file="$(plan_filename "$number" "$title")"
    echo "Claiming plan filename: $plan_file"
    body="$(set_plan_file "$body" "$plan_file")"
    body="$(set_turn "$body" "$TURN")"
    set_issue_body "$ISSUE_NUMBER" "$body"
  fi

  local discovery
  discovery="$(discovery_comment "$ISSUE_NUMBER")"
  if [ -z "$discovery" ]; then
    block_issue "$ISSUE_NUMBER" "phase is 'plan' but no discovery comment was found - this should not happen, discovery always posts one before advancing the phase. Investigate manually."
    exit 0
  fi

  local prompt
  prompt="Write $plan_file - a plan for the feature described below, using the discovery findings that follow it.

The file MUST have this structure, in this order:

## Packages
(bullet list of every existing package this change touches)

## Migration
(the migration file name(s) this change needs, or \"None\" if no schema change is needed)

## CI Checks
(bullet list of which checks from CLAUDE.md's \"How to Verify a Change\" section this change will have to pass - go build, go vet, go test, the RLS/permission audit, i18n check, license header check, etc: whichever actually apply, not all of them by default)

## Steps
(a checklist: every line MUST be exactly \"- [ ] <step>\". Each step is handed to a FRESH turn with NO other context from this plan and no memory of any other step, so each step must be entirely self-contained: name the exact file(s) it touches, say precisely what changes, and restate any constraint that step must respect - RLS policy wording, license header, i18n key, permission tag, whatever CLAUDE.md's never-violate rules require for that specific change. A step that only makes sense in the context of the rest of the plan will fail when executed alone - that is the single most important property of this file.)

Feature (from the issue):
$body

Discovery findings:
$discovery"

  run_aider_plan "$prompt" "$plan_file"

  if ! worktree_has_changes; then
    if aider_hit_quota_wall; then
      echo "Gemini returned a persistent quota error during plan generation. Stopping without retrigger; the schedule trigger will pick this issue back up."
      body="$(set_turn "$body" "$TURN")"
      set_issue_body "$ISSUE_NUMBER" "$body"
      post_comment "$ISSUE_NUMBER" "⏸️ Turn $TURN: Gemini's free-tier quota is exhausted during plan generation. Not counted as a failed attempt. The scheduled run will retry automatically."
      ACTION="budget_exhausted"
      RETRIGGER="false"
      exit 0
    fi
    echo "aider produced no changes for the plan and no quota error was logged - treating as a failed attempt."
  else
    local changed_files
    changed_files="$(git status --porcelain | awk '{print $NF}')"
    local plan_content
    if [ "$changed_files" != "$plan_file" ] || [ ! -f "$plan_file" ] || ! plan_content="$(cat "$plan_file")" || ! plan_steps "$plan_content" >/dev/null 2>&1; then
      echo "aider either touched something other than $plan_file, or $plan_file has no valid ## Steps section - discarding and treating as a failed attempt. Changed: $changed_files"
      git checkout -- .
      git clean -fd
    else
      local n_steps
      n_steps="$(plan_steps "$plan_content" | grep -c '^- \[')"
      if [ "$n_steps" -gt "$MAX_ITEMS" ]; then
        git checkout -- .
        git clean -fd
        block_issue "$ISSUE_NUMBER" "the plan has $n_steps steps, over the $MAX_ITEMS-item cap. Split the feature into smaller issues, then re-add $LABEL_GO on each."
        exit 0
      fi

      git add "$plan_file"
      git commit -m "agent: plan for #$ISSUE_NUMBER"
      git push -u origin "$plan_branch"
      ensure_plan_pr "$ISSUE_NUMBER" "$plan_branch" "$plan_file"

      local plan_pr_num
      plan_pr_num="$(pr_number_for_branch "$plan_branch")"

      body="$(set_phase "$body" awaiting-plan)"
      body="$(set_turn "$body" "$TURN")"
      set_issue_body "$ISSUE_NUMBER" "$body"
      post_comment "$ISSUE_NUMBER" "📋 Plan ready for review: PR #$plan_pr_num (\`$plan_file\`, $n_steps step(s)). Merge it to start implementation - the agent stops here until you do."
      ACTION="plan_complete"
      RETRIGGER="false"
      exit 0
    fi
  fi

  # Failed attempt (no diff, wrong file(s) touched, or malformed plan).
  body="$(set_phase_attempts "$body" plan "$attempts")"
  body="$(set_turn "$body" "$TURN")"
  set_issue_body "$ISSUE_NUMBER" "$body"
  if [ "$attempts" -ge "$MAX_PHASE_ATTEMPTS" ]; then
    block_issue "$ISSUE_NUMBER" "plan generation failed $attempts times: no valid $plan_file was produced. See turn $TURN's job log."
    exit 0
  fi
  post_comment "$ISSUE_NUMBER" "⚠️ Turn $TURN: plan attempt $attempts/$MAX_PHASE_ATTEMPTS failed (no valid plan file produced). Will retry."
  ACTION="failed_phase_retry"
  RETRIGGER="true"
  retrigger_self
}

# Polls whether the plan file has landed on main (merged). No attempt
# counter here on purpose - unlike discovery/plan, this phase's failure
# modes (plan not merged yet; a merged-but-malformed plan; a merged plan
# over the item cap) are either "nothing to do yet, check later" or
# terminal conditions a human needs to fix, never something a same-turn
# retry could resolve.
run_awaiting_plan_phase() {
  local plan_file
  plan_file="$(get_plan_file "$body")"
  if [ -z "$plan_file" ]; then
    block_issue "$ISSUE_NUMBER" "phase is 'awaiting-plan' but no plan_file is recorded in state - this should not happen. Investigate manually."
    exit 0
  fi

  if [ ! -f "$plan_file" ]; then
    echo "$plan_file does not exist on main yet - the plan PR hasn't been merged. Exiting cleanly; the schedule trigger or the docs/plans push trigger will check again."
    ACTION="awaiting_plan_merge"
    RETRIGGER="false"
    exit 0
  fi

  local plan_content steps
  plan_content="$(cat "$plan_file")"
  if ! steps="$(plan_steps "$plan_content")"; then
    block_issue "$ISSUE_NUMBER" "$plan_file exists on main but has no valid ## Steps section. It may have been edited into an invalid shape before merging - fix the file on main, or edit the issue's state block to set phase=plan to regenerate it, then re-add $LABEL_GO."
    exit 0
  fi

  local n_steps
  n_steps="$(grep -c '^- \[' <<<"$steps")"
  if [ "$n_steps" -gt "$MAX_ITEMS" ]; then
    block_issue "$ISSUE_NUMBER" "$plan_file has $n_steps steps after merging, over the $MAX_ITEMS-item cap - it may have been edited to add steps before merging. Split the work or edit the checklist by hand."
    exit 0
  fi

  body="$(set_checklist_from_lines "$body" "$steps")"
  body="$(set_phase "$body" implement)"
  body="$(set_turn "$body" "$TURN")"
  set_issue_body "$ISSUE_NUMBER" "$body"
  post_comment "$ISSUE_NUMBER" "▶️ Plan merged. Picked up $n_steps step(s) from \`$plan_file\` into the checklist - implementation starts now, one step per turn."
  ACTION="plan_merged_checklist_populated"
  RETRIGGER="true"
  retrigger_self
}

# Step 3a, unchanged: one checklist item per turn, on the issue's own
# agent/issue-<n> branch/draft PR (always distinct from the plan's
# agent/plan-<n> branch/PR).
run_implement_phase() {
  local branch="agent/issue-$ISSUE_NUMBER"

  # Both idempotent: safe on every turn, which is what lets a chain that
  # died between "branch pushed" and "PR created" - on turn 1 or any
  # other turn - recover on its next attempt instead of getting stuck.
  ensure_branch "$branch"
  ensure_pr "$ISSUE_NUMBER" "$branch"

  # Finished?
  local item_index
  if ! item_index="$(first_unchecked_index "$body")"; then
    local pr_num
    pr_num="$(pr_number_for_branch "$branch")"
    if [ -z "$pr_num" ]; then
      block_issue "$ISSUE_NUMBER" "every checklist item is checked, but no PR could be found for $branch even after ensure_pr. This should not happen - investigate manually."
      exit 0
    fi
    if run_fast_gate; then
      gh_or_block pr ready "$pr_num" --repo "$REPO"
      body="$(set_turn "$body" "$TURN")"
      set_issue_body "$ISSUE_NUMBER" "$body"
      gh_or_block issue edit "$ISSUE_NUMBER" --repo "$REPO" --remove-label "$LABEL_GO" --add-label "$LABEL_DONE"
      post_comment "$ISSUE_NUMBER" "✅ All checklist items complete after $TURN turns. PR #$pr_num is marked ready for review. The agent stops here - merging is your call."
      ACTION="finished"
    else
      block_issue "$ISSUE_NUMBER" "every checklist item is checked, but the fast gate (build/vet/test/license) is red on the final result. PR #$pr_num is left as a draft - see its CI/checks for the failure."
    fi
    RETRIGGER="false"
    exit 0
  fi
  ITEM_INDEX="$item_index"

  local item_text
  item_text="$(item_text_at "$body" "$ITEM_INDEX")"
  if [ -z "$item_text" ]; then
    block_issue "$ISSUE_NUMBER" "checklist item $ITEM_INDEX has empty text after stripping the checkbox marker - malformed checklist."
    exit 0
  fi

  ITEM_ATTEMPTS="$(get_item_attempts "$body" "$ITEM_INDEX")"
  ITEM_ATTEMPTS=$((ITEM_ATTEMPTS + 1))

  echo "Turn $TURN: issue #$ISSUE_NUMBER, item $ITEM_INDEX/$ITEM_TOTAL, attempt $ITEM_ATTEMPTS/$MAX_ITEM_ATTEMPTS"
  echo "Item text: $item_text"

  run_aider_on_item "$item_text"

  if ! worktree_has_changes; then
    # aider produced no changes at all this turn.
    if aider_hit_quota_wall; then
      echo "Gemini returned a persistent quota error this turn. Stopping without retrigger; the schedule trigger will pick this issue back up."
      body="$(set_turn "$body" "$TURN")"
      set_issue_body "$ISSUE_NUMBER" "$body"
      post_comment "$ISSUE_NUMBER" "⏸️ Turn $TURN: Gemini's free-tier quota is exhausted for item $ITEM_INDEX/$ITEM_TOTAL ($item_text). Not counted as a failed attempt. The scheduled run will retry automatically; no action needed."
      ACTION="budget_exhausted"
      RETRIGGER="false"
      exit 0
    fi
    echo "aider produced no changes and no quota error was logged - treating as a failed attempt."
  elif ! run_fast_gate; then
    echo "aider produced changes but the fast gate is red - discarding and treating as a failed attempt."
    git checkout -- .
    git clean -fd
  else
    # Success: commit, push, tick the box.
    git add -A
    git commit -m "agent: item $ITEM_INDEX/$ITEM_TOTAL - $item_text"
    git push origin "$branch"
    body="$(tick_item "$body" "$ITEM_INDEX")"
    body="$(set_turn "$body" "$TURN")"
    set_issue_body "$ISSUE_NUMBER" "$body"
    post_comment "$ISSUE_NUMBER" "✅ Turn $TURN: completed item $ITEM_INDEX/$ITEM_TOTAL: $item_text"
    ACTION="completed_item"
    RETRIGGER="true"
    retrigger_self
    exit 0
  fi

  # Failed attempt (no diff, or gate red). Record it and decide whether
  # to retry or give up on this item.
  body="$(set_item_attempts "$body" "$ITEM_INDEX" "$ITEM_ATTEMPTS")"
  body="$(set_turn "$body" "$TURN")"
  set_issue_body "$ISSUE_NUMBER" "$body"

  if [ "$ITEM_ATTEMPTS" -ge "$MAX_ITEM_ATTEMPTS" ]; then
    block_issue "$ISSUE_NUMBER" "item $ITEM_INDEX/$ITEM_TOTAL failed $ITEM_ATTEMPTS times: $item_text. See turn $TURN's job log for aider's output."
    exit 0
  fi

  post_comment "$ISSUE_NUMBER" "⚠️ Turn $TURN: item $ITEM_INDEX/$ITEM_TOTAL failed (attempt $ITEM_ATTEMPTS/$MAX_ITEM_ATTEMPTS): $item_text. Will retry."
  ACTION="failed_item_retry"
  RETRIGGER="true"
  retrigger_self
}

# --- main -----------------------------------------------------------------

main() {
  guard_run_rate

  # Concurrency: the workflow's own `concurrency: group: agent-loop` is
  # the primary guarantee (only one run of this workflow executes at a
  # time; a second trigger queues rather than overlapping). This is a
  # second, explicit check on top of it, since the loop retriggers
  # itself and a scheduled run can also land at any time - cheap
  # insurance against relying on exactly one mechanism for something
  # that runs unattended.
  local other_runs
  other_runs="$(gh run list --repo "$REPO" --workflow "$WORKFLOW_FILE" --json databaseId,status \
    -q '[.[] | select(.status == "in_progress" or .status == "queued") | .databaseId] | map(select(. != '"${GITHUB_RUN_ID:?}"')) | .[]')"
  if [ -n "$other_runs" ]; then
    echo "Another run of $WORKFLOW_FILE is already in progress or queued (id(s): $other_runs). Exiting without action." >&2
    ACTION="concurrent_run_detected"
    RETRIGGER="false"
    exit 0
  fi

  # Input validation (same spirit as agent-smoke.yml's swap-check: fail
  # fast on a malformed input before doing anything expensive).
  local issue_input="${INPUT_ISSUE:-}"
  if [ -n "$issue_input" ] && ! [[ "$issue_input" =~ ^[0-9]+$ ]]; then
    echo "issue input '$issue_input' is not a bare issue number." >&2
    exit 1
  fi

  # 1. find the single active issue.
  if [ -n "$issue_input" ]; then
    ISSUE_NUMBER="$issue_input"
  elif ! ISSUE_NUMBER="$(find_active_issue)"; then
    echo "No single active $LABEL_GO issue found. Exiting cleanly."
    ACTION="no_active_issue"
    RETRIGGER="false"
    exit 0
  fi

  # body/labels are deliberately script-global (no `local`), like
  # ISSUE_NUMBER/TURN/etc above - every phase function reads and mutates
  # them, and this is a single linear turn, not something that benefits
  # from threading them through as parameters everywhere.
  body="$(issue_body "$ISSUE_NUMBER")"
  labels="$(issue_labels "$ISSUE_NUMBER")"

  # Kill switch.
  if ! grep -qx "$LABEL_GO" <<<"$labels"; then
    echo "Issue #$ISSUE_NUMBER no longer carries $LABEL_GO. Exiting without retrigger."
    ACTION="kill_switch"
    RETRIGGER="false"
    exit 0
  fi
  if grep -qx "$LABEL_PAUSED" <<<"$labels"; then
    echo "Issue #$ISSUE_NUMBER carries $LABEL_PAUSED. Exiting without retrigger."
    ACTION="kill_switch"
    RETRIGGER="false"
    exit 0
  fi

  # Checklist cap. A no-op for discovery/plan/awaiting-plan, which have
  # no checklist yet (checklist_total is 0, never > MAX_ITEMS) - the
  # equivalent check for a freshly-parsed plan's step count happens in
  # run_awaiting_plan_phase, against the NEW checklist before it's
  # written, and again as a discard-and-block in run_plan_phase right
  # after generation, so an oversized plan never even reaches merge.
  ITEM_TOTAL="$(checklist_total "$body")"
  if [ "$ITEM_TOTAL" -gt "$MAX_ITEMS" ]; then
    block_issue "$ISSUE_NUMBER" "checklist has $ITEM_TOTAL items, over the $MAX_ITEMS-item cap. Split the issue or edit the checklist, then re-add $LABEL_GO."
    exit 0
  fi

  # Turn counter - counts every turn regardless of phase.
  TURN="$(get_turn "$body")"
  TURN=$((TURN + 1))
  if [ "$TURN" -gt "$MAX_TURNS" ]; then
    block_issue "$ISSUE_NUMBER" "turn $TURN exceeds the $MAX_TURNS-turn cap."
    exit 0
  fi

  git config user.name "zonaryos-agent"
  git config user.email "agent@users.noreply.github.com"

  PHASE="$(get_phase "$body")"
  case "$PHASE" in
    discovery) run_discovery_phase ;;
    plan) run_plan_phase ;;
    awaiting-plan) run_awaiting_plan_phase ;;
    implement) run_implement_phase ;;
    *)
      block_issue "$ISSUE_NUMBER" "unknown phase '$PHASE' in the issue's state block - this should not happen. Fix the <!-- agent-state --> block by hand (valid values: discovery, plan, awaiting-plan, implement) and re-add $LABEL_GO."
      exit 0
      ;;
  esac
}

# The self-trigger. Every phase function calls it from at most one
# branch per invocation, and always as either the last statement in that
# function or immediately followed by `exit` - so across the whole
# script (find_active_issue -> exactly one phase function runs per
# turn -> that function returns or exits), retrigger_self can fire at
# most once per process. Current call sites: run_discovery_phase's
# success and failed-retry paths, run_plan_phase's failed-retry path
# (success deliberately does NOT retrigger - the chain waits for a human
# merge), run_awaiting_plan_phase's success path, and
# run_implement_phase's completed-item and failed-retry paths.
#
# workflow_dispatch and repository_dispatch are the documented exception
# to "the default GITHUB_TOKEN cannot start another workflow run", which
# is what makes this work with no PAT and no GitHub App.
retrigger_self() {
  gh_or_block workflow run "$WORKFLOW_FILE" --repo "$REPO" -f "issue=$ISSUE_NUMBER"
}

# Guarded so this file can be `source`d by a test harness (see
# tests/test_gh_or_block.sh, tests/test_state_verification.sh,
# tests/test_guard_run_rate.sh) to reach individual functions without
# running the whole loop.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  main "$@"
fi
