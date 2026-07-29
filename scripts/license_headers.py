#!/usr/bin/env python3
"""License Header Check (CI Checklist item 7).

Verifies every tracked Go source file under cmd/ and internal/, and every
tracked TS/TSX source file under web/src/, starts with the license header
in scripts/license-header.txt (as a "//" comment block) - see LICENSE (a
draft, pending legal review - docs/OPEN_POINTS.md item 20) and CLAUDE.md's
Repository Map entry for it.

For a Go file, the header must be the very first thing in the file (with
a blank line after it) so it never attaches as that file's package doc
comment - Go only treats a comment block as the package doc comment when
it directly touches the `package` clause with no blank line in between,
so inserting our header as its own block, separated by a blank line from
whatever follows, never disturbs an existing package doc comment.

For a TS/TSX file that opens with a "use client"/"use server" directive,
that directive must remain the literal first line (a JS/TS requirement),
so the header is expected immediately after it instead of before it.

Usage:
    python3 scripts/license_headers.py --check   # CI: exit 1 on any violation
    python3 scripts/license_headers.py --fix     # rewrite files to add a
                                                  # missing header
"""
import sys
import re
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
HEADER_TEXT_PATH = REPO_ROOT / "scripts" / "license-header.txt"

DIRECTIVE_RE = re.compile(r'^[\'"]use (client|server)[\'"];?\s*$')


def header_lines():
    lines = HEADER_TEXT_PATH.read_text(encoding="utf-8").splitlines()
    return [f"// {line}" if line else "//" for line in lines]


def header_block():
    return "\n".join(header_lines()) + "\n"


def target_files():
    files = []
    for base in ("cmd", "internal"):
        files.extend((REPO_ROOT / base).rglob("*.go"))
    files.extend((REPO_ROOT / "web" / "src").rglob("*.ts"))
    files.extend((REPO_ROOT / "web" / "src").rglob("*.tsx"))
    return sorted(files)


def has_header(text: str) -> bool:
    lines = text.splitlines()
    idx = 0
    # A directive prologue, if present, must be skipped before looking for
    # the header - it must stay the file's literal first line.
    if idx < len(lines) and DIRECTIVE_RE.match(lines[idx].strip()):
        idx += 1
        # allow a blank line after the directive, same as after the header
        if idx < len(lines) and lines[idx].strip() == "":
            idx += 1
    expected = header_lines()
    return lines[idx:idx + len(expected)] == expected


def insert_header(text: str) -> str:
    lines = text.splitlines(keepends=True)
    if lines and DIRECTIVE_RE.match(lines[0].strip()):
        directive = lines[0]
        rest = lines[1:]
        return directive + header_block() + "\n" + "".join(rest)
    return header_block() + "\n" + text


def main():
    if len(sys.argv) != 2 or sys.argv[1] not in ("--check", "--fix"):
        print("usage: license_headers.py --check|--fix", file=sys.stderr)
        return 2

    mode = sys.argv[1]
    violations = []
    for path in target_files():
        text = path.read_text(encoding="utf-8")
        if has_header(text):
            continue
        if mode == "--fix":
            path.write_text(insert_header(text), encoding="utf-8")
            print(f"added license header: {path.relative_to(REPO_ROOT)}")
        else:
            violations.append(path.relative_to(REPO_ROOT))

    if mode == "--check":
        if violations:
            print("Missing license header in:", file=sys.stderr)
            for v in violations:
                print(f"  {v}", file=sys.stderr)
            print(
                f"\n{len(violations)} file(s) are missing the license header "
                f"from scripts/license-header.txt. Run "
                f"'python3 scripts/license_headers.py --fix' to add it.",
                file=sys.stderr,
            )
            return 1
        print("license header check passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
