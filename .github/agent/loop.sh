#!/usr/bin/env bash
# Step 3a: self-triggering agent loop, orchestration only.
#
# One issue = one feature = one branch = one draft PR. This script does
# exactly ONE checklist item (or one bookkeeping step) per invocation,
# then either re-triggers itself via `gh workflow run` or stops. All
# checklist/state text handling lives in lib.sh, which is unit-tested
# offline (.github/agent/tests/run_tests.sh) - this file is the thin,
# mostly-untestable-without-a-real-run part: gh/git/aider calls. Three
# pieces of THIS file's own control flow ARE covered offline against a
# mocked `gh` (never touches the network): gh_or_block/block_issue's
# failure handling (tests/test_gh_or_block.sh), set_issue_body's
# verify-after-write check (tests/test_state_verification.sh), and
# guard_run_rate's threshold logic (tests/test_guard_run_rate.sh) - see
# each file's own header for exactly what it does and does not prove.
#
# Deliberately NOT here (step 3b): any LLM planning, checklist authoring,
# or research. The checklist is hand-written in the issue body; aider
# only ever executes one already-specified item. If this loop breaks, we
# need to be able to tell plumbing from intelligence, which is only
# possible if there is no intelligence in this file yet.
#
# TODO(step 3b): --yes-always below is what let a swapped-input smoke-test
# run (see .github/workflows/agent-smoke.yml's history) create files from
# bogus arguments without asking. For 3a the checklist item text is
# written by whoever applied agent:go - a maintainer, the same trust
# level as a manual workflow_dispatch. Step 3b's checklist can come from
# LLM-authored or issue-comment-sourced text, i.e. genuinely untrusted
# input reaching --yes-always for the first time. That needs a real guard
# before 3b lands, not just this comment.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./lib.sh
source "$HERE/lib.sh"

REPO="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must be set}"
WORKFLOW_FILE="agent-loop.yml"
MAX_TURNS=80
MAX_ITEMS=60
MAX_ITEM_ATTEMPTS=3
MODEL="gemini/gemini-3.5-flash-lite"
EDIT_FORMAT="diff-fenced"
MAP_TOKENS=4096
AIDER_LOG="/tmp/agent-loop-aider.log"

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
ITEM_INDEX=""
ITEM_TOTAL=""
ITEM_ATTEMPTS=""
ACTION="unknown"
RETRIGGER="false"
LAST_GH_CMD=""

print_resolved_state() {
  echo "RESOLVED STATE: issue=#${ISSUE_NUMBER:-none} turn=${TURN:-?} item=${ITEM_INDEX:-?}/${ITEM_TOTAL:-?} attempts=${ITEM_ATTEMPTS:-?} action=${ACTION} retrigger=${RETRIGGER} last_gh_cmd=[${LAST_GH_CMD:-none}]"
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
# trip, not in lib.sh's parsing - and it's not reproducible in an
# environment with no gh CLI, so it isn't root-caused here. What matters
# operationally: MAX_TURNS and the per-item 3-strikes cap both read
# get_turn/get_item_attempts from a freshly re-fetched body every run,
# so if that write silently doesn't take, BOTH brakes read 0 forever and
# neither can ever fire - and gh_or_block's error handling does not
# catch this class of failure, because nothing about the write itself
# returns an error. Silent state loss is exactly the condition under
# which a self-triggering loop is unbounded, so it has to be loud: catch
# it here, immediately after every write, and stop hard rather than
# franchise the same unverified assumption through 80 more turns.
set_issue_body() {
  local issue="$1" body="$2"
  local expected_turn
  expected_turn="$(get_turn "$body")"

  gh_or_block issue edit "$issue" --repo "$REPO" --body "$body" >/dev/null

  local roundtrip actual_turn
  roundtrip="$(issue_body "$issue")"
  actual_turn="$(get_turn "$roundtrip")"

  if [ "$actual_turn" != "$expected_turn" ]; then
    echo "State verification failed on #$issue: wrote turn=$expected_turn but reading the issue back shows turn=$actual_turn (or no <!-- agent-state --> block at all). MAX_TURNS and the 3-strikes cap both depend on this write surviving, so this is a hard stop, not a retry." >&2
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
# callers must check for explicitly - see ensure_pr and main()'s finished
# branch).
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

# --- aider ------------------------------------------------------------

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
    --verbose \
    | tee "$AIDER_LOG"
  set -e
  echo "--- rate-limit/quota lines surfaced by this call, if any ---"
  grep -iE 'ratelimit|rate-limit|retry-after|quota|RESOURCE_EXHAUSTED' "$AIDER_LOG" || echo "(none)"
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
# 3-strikes cap both read state this loop wrote to the issue body on a
# previous run, and issue #73's validation chain showed that write can
# silently not persist - which means every brake built on top of it can
# fail at once, without an error anywhere. This one reads GitHub Actions'
# own run history instead, which the loop cannot corrupt by writing bad
# state, so it still catches a runaway chain even if state persistence
# is broken again in some new way this PR didn't anticipate. Deliberately
# crude (a flat count in a time window, no issue awareness, no nuance)
# and deliberately not routed through gh_or_block/block_issue - sharing
# no machinery with the other brakes is the whole point.
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

  local body labels
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

  # Checklist cap.
  ITEM_TOTAL="$(checklist_total "$body")"
  if [ "$ITEM_TOTAL" -gt "$MAX_ITEMS" ]; then
    block_issue "$ISSUE_NUMBER" "checklist has $ITEM_TOTAL items, over the $MAX_ITEMS-item cap. Split the issue or edit the checklist, then re-add $LABEL_GO."
    exit 0
  fi
  if [ "$ITEM_TOTAL" -eq 0 ]; then
    block_issue "$ISSUE_NUMBER" "no \`## Checklist\` items found in the issue body. Nothing to do."
    exit 0
  fi

  # Turn counter.
  TURN="$(get_turn "$body")"
  TURN=$((TURN + 1))
  if [ "$TURN" -gt "$MAX_TURNS" ]; then
    block_issue "$ISSUE_NUMBER" "turn $TURN exceeds the $MAX_TURNS-turn cap."
    exit 0
  fi

  local branch="agent/issue-$ISSUE_NUMBER"

  git config user.name "zonaryos-agent"
  git config user.email "agent@users.noreply.github.com"

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

# The self-trigger. Called from exactly two places in main(): the
# "completed_item" branch (immediately followed by exit 0) and the
# "failed_item_retry" branch (the last statement in main(), nothing
# follows it). Every other branch in main() - no active issue, kill
# switch, checklist cap, turn cap, quota wall, 3-strikes block, finished
# - exits (or falls through to the end of main()) without ever reaching
# either call site. Because both call sites are terminal (nothing in
# main() runs after either one, and main() itself only runs once per
# process), a single invocation of this script can call retrigger_self
# at most once - there's no branch or loop that could reach it twice.
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
