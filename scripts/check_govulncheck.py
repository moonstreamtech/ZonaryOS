#!/usr/bin/env python3
"""CI Checklist item 10, "Dependency Vulnerability Scan" (Go/backend half -
see web/scripts/check-npm-audit.mjs for the frontend half).

Runs `govulncheck -json ./...` (govulncheck's default "source" scan mode,
which already restricts findings to vulnerabilities actually reachable from
this module's own call graph - not merely-imported-but-unused ones) and
fails on any finding whose OSV id isn't listed in
scripts/govulncheck-allowlist.json - a documented exception, not a silent
ignore, same convention as the frontend's audit-allowlist.json. Each
allowlist entry must carry a `reason` and a `reviewBy` date.

govulncheck's JSON output is a *stream* of concatenated top-level JSON
objects (its own documented protocol - see
golang.org/x/vuln/internal/govulncheck.Message), not one JSON array or
newline-delimited JSON, so this parses it with repeated
json.JSONDecoder.raw_decode calls rather than json.load/loads.

A Finding is only treated as actionable here when its first trace frame
has a non-empty "function" - the same "symbol actually called" bar
govulncheck's own default text-mode output uses for its "Vulnerabilities
found" section, as opposed to a merely-imported-module-level finding.

Usage:
    python3 scripts/check_govulncheck.py
"""
import json
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
ALLOWLIST_PATH = REPO_ROOT / "scripts" / "govulncheck-allowlist.json"

# Pinned so this check's behavior doesn't silently shift with a new
# govulncheck release - bump deliberately, same as any other dependency.
GOVULNCHECK_VERSION = "v1.6.0"


def load_allowlist():
    entries = json.loads(ALLOWLIST_PATH.read_text(encoding="utf-8"))
    now = datetime.now(timezone.utc)
    by_id = {}
    expired = []
    for entry in entries:
        for field in ("id", "reason", "reviewBy"):
            if field not in entry:
                raise SystemExit(f"govulncheck-allowlist.json entry missing {field!r}: {entry}")
        review_by = datetime.fromisoformat(entry["reviewBy"]).replace(tzinfo=timezone.utc)
        if review_by < now:
            expired.append(entry["id"])
        by_id[entry["id"]] = entry
    return by_id, expired


def parse_message_stream(text):
    decoder = json.JSONDecoder()
    idx = 0
    messages = []
    n = len(text)
    while idx < n:
        while idx < n and text[idx].isspace():
            idx += 1
        if idx >= n:
            break
        obj, end = decoder.raw_decode(text, idx)
        messages.append(obj)
        idx = end
    return messages


def run_govulncheck():
    proc = subprocess.run(
        ["go", "run", f"golang.org/x/vuln/cmd/govulncheck@{GOVULNCHECK_VERSION}", "-json", "./..."],
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
    )
    # govulncheck's own exit codes: 0 = clean, 3 = vulnerabilities found,
    # anything else is a tool/environment failure (e.g. it couldn't reach
    # the vulnerability database) - never treat that as "no findings".
    if proc.returncode not in (0, 3):
        print(proc.stdout, file=sys.stderr)
        print(proc.stderr, file=sys.stderr)
        print(
            f"\ngovulncheck exited {proc.returncode} (neither 0=clean nor 3=vulnerabilities-found) - "
            f"treat this as a tool/environment failure, not a clean scan.",
            file=sys.stderr,
        )
        sys.exit(2)
    return proc.stdout


def actionable_findings(messages):
    findings = []
    for msg in messages:
        finding = msg.get("finding")
        if not finding:
            continue
        trace = finding.get("trace") or []
        if trace and trace[0].get("function"):
            findings.append(finding)
    return findings


def main():
    allowlist, expired = load_allowlist()
    if expired:
        print(
            "govulncheck-allowlist.json has expired reviewBy entries - re-review, don't just push the date out silently:",
            file=sys.stderr,
        )
        for osv_id in expired:
            print(f"  - {osv_id}", file=sys.stderr)

    stdout = run_govulncheck()
    messages = parse_message_stream(stdout)
    findings = actionable_findings(messages)

    unallowlisted = [f for f in findings if f["osv"] not in allowlist]
    allowlisted = [f for f in findings if f["osv"] in allowlist]

    if allowlisted:
        print(f"{len(allowlisted)} finding(s) suppressed via scripts/govulncheck-allowlist.json:")
        for f in allowlisted:
            print(f"  - {f['osv']} (fixed in {f.get('fixed_version', 'unknown')})")

    if unallowlisted:
        print("\nUnallowlisted vulnerabilities found:", file=sys.stderr)
        for f in unallowlisted:
            print(f"  - {f['osv']} (fixed in {f.get('fixed_version', 'unknown')})", file=sys.stderr)
        print(
            "\nFix these (upgrade the affected module), or if there is genuinely no fix available yet, "
            "add a documented entry (id, reason, reviewBy) to scripts/govulncheck-allowlist.json - "
            "do not silently ignore them.",
            file=sys.stderr,
        )
        return 1

    if expired:
        return 1

    print("\ngovulncheck check passed (no unallowlisted vulnerabilities)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
