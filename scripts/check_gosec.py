#!/usr/bin/env python3
"""CI Checklist item 11, "SAST Security Scan" (Go/backend half - see
scripts/check_gitleaks.sh for the secret-scanning half).

Runs gosec (github.com/securego/gosec, Apache-2.0, purely local static
analysis - no external service, no login, no API token) against the
whole module and fails on any finding. Deliberately no severity threshold
or allowlist file here (unlike the dependency-vulnerability checks): gosec
findings are few and specific enough in this codebase that the right
response to a real finding is either fix it or suppress the single line
with gosec's own native `// #nosec <RULE> -- <reason>` comment - which is
already a documented, code-reviewed exception mechanism, so a second,
parallel allowlist file would just be redundant machinery.

Usage:
    python3 scripts/check_gosec.py
"""
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
GOSEC_VERSION = "v2.28.0"


def main():
    proc = subprocess.run(
        ["go", "run", f"github.com/securego/gosec/v2/cmd/gosec@{GOSEC_VERSION}", "-fmt=text", "./..."],
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
    )
    print(proc.stdout)
    if proc.returncode == 0:
        print("gosec check passed (no findings)")
        return 0

    print(proc.stderr, file=sys.stderr)
    print(
        f"\ngosec exited {proc.returncode} - fix the finding(s) above, or if it's a genuine false "
        f"positive, suppress that one line with a '// #nosec <RULE> -- <reason>' comment.",
        file=sys.stderr,
    )
    return 1


if __name__ == "__main__":
    sys.exit(main())
