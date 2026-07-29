# ZonaryOS — Root Guide

This file is the entry point for any AI agent (Claude Code or otherwise) working in this repository. Read this first, then follow the pointers below. Do not assume anything not stated here or in the linked docs — ask instead of guessing.

## What This Project Is

ZonaryOS is a universal, sector-agnostic ERP/WMS/MES system kernel. Full product vision: `docs/VISION.md`.

## Repository Map

- `docs/VISION.md` — finalized product scope and architecture decisions. Source of truth for **what** to build and **why**.
- `docs/RULES.md` — how decisions get made and documented for this project (mostly relevant to planning conversations, not code, but includes naming/privacy conventions that also apply to code comments and commit messages).
- `docs/OPEN_POINTS.md` — tracked list of unresolved design questions. If a task touches one of these, flag it — don't assume an answer.
- `LICENSE` — commercial-use-restricted license (see Vision §5). Draft pending legal review — do not treat as final legal text.
- `README.md` — public-facing project description.

Live tracking (task/PR status, CI checklist, open points board) lives in Notion, not in this repo.

## Never-Violate Rules

These are architecture/process decisions from `docs/VISION.md` that must not be silently reversed or worked around:

1. **All repository content is English.** Code, comments, commit messages, PR descriptions, docs, issues — no exceptions.
2. **No self-hosted deployment path.** The software must remain functionally inert without a valid runtime license check against ZonaryOS's central license service. Do not add code that makes the system fully operable without this check.
3. **PostgreSQL Row-Level Security (RLS) is mandatory** on every tenant-scoped table. No table may rely solely on application-level `tenant_id` filtering for isolation.
4. **No hardcoded UI strings.** All user-facing text goes through the i18n layer from the start.
5. **Backend is Go, modular monolith — not microservices.** Modules must have clear internal package/interface boundaries, but ship as a single deployable binary unless a documented decision changes this.
6. **No breaking API changes without a deprecation cycle.** The API schema is versioned and diffed in CI.
7. **Every permission-gated UI element must carry a permission tag.** Untagged interactive elements should be flagged, not silently shipped (see Vision §3, "Permission Audit Mode").
8. **Autonomous decision rules (workflow engine) are deterministic, not ML-based**, and every rule has an explicit autonomous/human-approval toggle set by the end user — never hardcoded to always-autonomous.
9. **The AI assistant layer (in-product AI) never makes changes on its own.** It only guides; the user always takes the final action. Do not build "auto-apply" behavior for this layer.

## How to Verify a Change

`.github/workflows/ci.yml` runs automatically on every PR (and on push to `main`) - see `docs/DEVELOPMENT.md`'s "Continuous Integration" section for what each job actually does. Before pushing, the same checks can be run locally:

```
go build ./...
go vet ./... && go test ./...
go run ./cmd/ciaudit                              # RLS / Permission Drift Audit
python3 scripts/license_headers.py --check         # License Header Check (--fix to add missing headers)
cd web && npm ci && npm run build && npm run lint && npm test && npm run check:i18n
```

Postgres-backed integration tests need a real Postgres (`make dev-up` or `make dev-up-standalone`, then `make migrate` - see `docs/DEVELOPMENT.md`). Doc Sync Check and API Contract Diff are PR-diff checks (`python3 scripts/check_doc_sync.py origin/main`, `python3 scripts/check_api_contract.py origin/main`).

Still "Not Set Up" (need more deploy-pipeline infrastructure first, not attempted yet): Migration Safety Check, Dependency Vulnerability Scan, SAST Security Scan, E2E Smoke Test, Canary/Rollback Trigger. Treat these as manual acceptance-bar items still, same as before CI existed at all.

## Where to Look for What

| Question type | Go to |
|---|---|
| "Is this in scope? What did we decide about X?" | `docs/VISION.md` |
| "Is this already answered somewhere, or still open?" | `docs/OPEN_POINTS.md` |
| "How should I document a new decision?" | `docs/RULES.md` |
| "What's the current build/task status?" | Notion Command Center (not in repo) |

If a task requires a decision not covered by any of the above, stop and ask — do not infer or invent product behavior.
