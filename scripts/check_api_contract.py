#!/usr/bin/env python3
"""API Contract Diff (CI Checklist item 8, Never-Violate Rule 6: "No
breaking API changes without a deprecation cycle. The API schema is
versioned and diffed in CI.").

Compares docs/api/openapi.yaml against the same file's content at a base
ref (the PR's base branch, typically origin/main) and fails if any path or
HTTP method documented in the base version is missing from the head
version - removing a documented path/method is unambiguously a breaking
change. This is a lightweight, path/method-level diff, not a full schema
diff (it will not catch a removed required request/response field, for
example) - a known limitation, not an oversight; see the CI pipeline's PR
description for what's intentionally out of scope for this first version.

Usage:
    python3 scripts/check_api_contract.py <base-ref>
"""
import re
import subprocess
import sys

FILE = "docs/api/openapi.yaml"
PATH_RE = re.compile(r"^  (/\S*):\s*$")
METHOD_RE = re.compile(r"^    (get|post|put|delete|patch|options|head):\s*$")


def content_at_ref(ref):
    try:
        return subprocess.check_output(
            ["git", "show", f"{ref}:{FILE}"], stderr=subprocess.DEVNULL
        ).decode()
    except subprocess.CalledProcessError:
        return None


def parse(text):
    paths = {}
    current = None
    for line in text.splitlines():
        m = PATH_RE.match(line)
        if m:
            current = m.group(1)
            paths[current] = set()
            continue
        m = METHOD_RE.match(line)
        if m and current is not None:
            paths[current].add(m.group(1))
    return paths


def main():
    if len(sys.argv) != 2:
        print("usage: check_api_contract.py <base-ref>", file=sys.stderr)
        return 2
    base_ref = sys.argv[1]

    base_text = content_at_ref(base_ref)
    if base_text is None:
        print(f"api contract diff: no {FILE} at {base_ref}, nothing to compare (new file)")
        return 0

    with open(FILE, encoding="utf-8") as f:
        head_text = f.read()

    base_paths = parse(base_text)
    head_paths = parse(head_text)

    failures = []
    for path, methods in base_paths.items():
        if path not in head_paths:
            failures.append(f"path {path} was removed")
            continue
        for method in sorted(methods):
            if method not in head_paths[path]:
                failures.append(f"{method.upper()} {path} was removed")

    if failures:
        print("API Contract Diff FAILED (Never-Violate Rule 6):", file=sys.stderr)
        for f in failures:
            print(f"  - {f}", file=sys.stderr)
        print(
            "\nNo breaking API changes without a deprecation cycle. If this removal "
            "already went through one, this check has no override mechanism yet - "
            "flag it in the PR description and treat this as a known gap, not a "
            "silent bypass.",
            file=sys.stderr,
        )
        return 1

    print("api contract diff passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
