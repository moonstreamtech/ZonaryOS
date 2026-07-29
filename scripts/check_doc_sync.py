#!/usr/bin/env python3
"""Doc Sync Check (CI Checklist item 6): if a module directory changed
(internal/**, cmd/**, or web/src/**), docs/DEVELOPMENT.md must have
changed in the same diff - per that doc's own per-module section
convention (a "## <Module>" section per internal/cmd/web module,
established across PRs #3-#10; see the CI section added for this PR too).

Usage:
    python3 scripts/check_doc_sync.py <base-ref> [head-ref]

head-ref defaults to HEAD (the current checkout).
"""
import subprocess
import sys

MODULE_PREFIXES = ("internal/", "cmd/", "web/src/")
DOC_FILE = "docs/DEVELOPMENT.md"


def changed_files(base_ref, head_ref):
    out = subprocess.check_output(
        ["git", "diff", "--name-only", f"{base_ref}...{head_ref}"]
    ).decode()
    return [line for line in out.splitlines() if line]


def main():
    if len(sys.argv) < 2:
        print("usage: check_doc_sync.py <base-ref> [head-ref]", file=sys.stderr)
        return 2

    base_ref = sys.argv[1]
    head_ref = sys.argv[2] if len(sys.argv) > 2 else "HEAD"

    files = changed_files(base_ref, head_ref)
    module_files = [f for f in files if f.startswith(MODULE_PREFIXES)]

    if module_files and DOC_FILE not in files:
        print("Doc Sync Check FAILED:", file=sys.stderr)
        print(
            f"  internal/, cmd/, or web/src/ files changed but {DOC_FILE} was not "
            f"updated in the same PR:",
            file=sys.stderr,
        )
        for f in module_files:
            print(f"    {f}", file=sys.stderr)
        print(
            f"\nUpdate the relevant section of {DOC_FILE} (or add a new one) to "
            f"describe this change - see its existing per-module sections for the "
            f"expected format.",
            file=sys.stderr,
        )
        return 1

    print("doc sync check passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
