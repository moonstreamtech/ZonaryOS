#!/usr/bin/env python3
"""CI Checklist item 9, "Migration Safety Check".

Vision §4's active-passive multi-region architecture (single global
primary + read-only replicas, automatic failover) means a rolling
deploy window can have old and new application code running against the
same schema simultaneously. A migration that isn't safely forward/
backward compatible during that window - dropping a column/table the
still-running old code reads, narrowing a column type, renaming a column
in place instead of an additive dual-write - is exactly the failure mode
this check exists to catch, per this project's migration tool
convention (golang-migrate, see internal/platform/db.Migrate and
migrations/*.up.sql/*.down.sql).

Checks, over every migrations/*.up.sql file added in the PR diff:
  1. DROP TABLE
  2. DROP COLUMN
  3. TRUNCATE
  4. ALTER COLUMN ... TYPE (a column type change - this check cannot
     statically tell a narrowing change from a widening one, so any TYPE
     change is flagged for manual review; a known limitation, not an
     oversight)
  5. RENAME COLUMN / RENAME TO (a non-additive rename - the old name
     disappears atomically, which old-code-still-running-against-new-
     schema can't tolerate)

...and separately, over the whole migrations/ directory: any migration
file that already existed at the base ref but was *modified* in this PR
(not just added) - editing a migration after it may have already run
elsewhere is itself the same class of rolling-deploy hazard.

A migration can acknowledge an intentional destructive change with a
`-- migration-safety:acknowledge: <reason>` comment anywhere in the file -
a documented exception, not a silent bypass; it still prints as
"acknowledged", not silently dropped from the output.

Usage:
    python3 scripts/check_migration_safety.py <base-ref> [head-ref]
"""
import re
import subprocess
import sys

ACKNOWLEDGE_MARKER = "migration-safety:acknowledge"

DESTRUCTIVE_PATTERNS = [
    ("DROP TABLE", re.compile(r"(?i)\bDROP\s+TABLE\b")),
    ("DROP COLUMN", re.compile(r"(?i)\bDROP\s+COLUMN\b")),
    ("TRUNCATE", re.compile(r"(?i)\bTRUNCATE\b")),
    ("ALTER COLUMN ... TYPE (possible narrowing)", re.compile(r"(?i)\bALTER\s+COLUMN\b[^;]*\bTYPE\b")),
    ("non-additive RENAME", re.compile(r"(?i)\bRENAME\s+(COLUMN|TO)\b")),
]


def git_diff_files(base_ref, head_ref, diff_filter):
    out = subprocess.check_output(
        ["git", "diff", "--name-only", f"--diff-filter={diff_filter}", f"{base_ref}...{head_ref}", "--", "migrations/*.up.sql"]
    ).decode()
    return [line for line in out.splitlines() if line]


def file_content_at(ref, path):
    return subprocess.check_output(["git", "show", f"{ref}:{path}"]).decode()


def scan_added_migration(path, text):
    findings = []
    for label, pattern in DESTRUCTIVE_PATTERNS:
        if pattern.search(text):
            findings.append(label)
    return findings


def main():
    if len(sys.argv) < 2:
        print("usage: check_migration_safety.py <base-ref> [head-ref]", file=sys.stderr)
        return 2
    base_ref = sys.argv[1]
    head_ref = sys.argv[2] if len(sys.argv) > 2 else "HEAD"

    added = git_diff_files(base_ref, head_ref, "A")
    modified = git_diff_files(base_ref, head_ref, "M")

    failures = []
    acknowledged = []

    for path in modified:
        failures.append(
            f"{path}: an existing migration was MODIFIED, not added - migrations must never be edited "
            f"after being merged (a rolling multi-region deploy may have already applied the old version "
            f"elsewhere - Vision §4). Add a new migration instead."
        )

    for path in added:
        with open(path, encoding="utf-8") as f:
            text = f.read()
        findings = scan_added_migration(path, text)
        if not findings:
            continue
        if ACKNOWLEDGE_MARKER in text:
            acknowledged.append((path, findings))
        else:
            for label in findings:
                failures.append(
                    f"{path}: contains {label} - not safely forward/backward compatible during a rolling "
                    f"primary/replica deploy window (Vision §4). If intentional, add a "
                    f"'-- {ACKNOWLEDGE_MARKER}: <reason>' comment to this file."
                )

    if acknowledged:
        print("Acknowledged destructive migration(s):")
        for path, findings in acknowledged:
            print(f"  - {path}: {', '.join(findings)}")

    if failures:
        print("Migration Safety Check FAILED:", file=sys.stderr)
        for f in failures:
            print(f"  - {f}", file=sys.stderr)
        return 1

    print("migration safety check passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
