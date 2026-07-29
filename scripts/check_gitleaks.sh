#!/usr/bin/env bash
# Copyright (c) ZonaryOS. All rights reserved.
# Use of this source code is governed by the license found in the LICENSE
# file in the root of this repository (draft, pending legal review - see
# docs/OPEN_POINTS.md item 20).

# CI Checklist item 11, "SAST Security Scan" (secret-scanning half - see
# scripts/check_gosec.py for the Go static-analysis half). Runs gitleaks
# (github.com/zricethezav/gitleaks, MIT, purely local regex/entropy
# analysis over git history - no external service, no login, no API
# token) and fails on any finding not covered by .gitleaks.toml's
# documented allowlist.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

if ! command -v gitleaks >/dev/null 2>&1; then
    echo "gitleaks is not installed - see docs/DEVELOPMENT.md's Continuous Integration section" >&2
    exit 2
fi

gitleaks detect --source=. --config=.gitleaks.toml -v
echo "gitleaks check passed"
