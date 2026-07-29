.PHONY: build test vet run migrate audit-purge ci-audit license-check license-fix web-dev web-build web-lint web-i18n-check dev-up dev-down dev-up-standalone dev-down-standalone

build:
	go build ./...

vet:
	go vet ./...

test:
	go test -p 1 ./...

run:
	go run ./cmd/server

migrate:
	go run ./cmd/migrate

# Manually-invoked maintenance command - not run by anything automatically.
# Requires ZONARYOS_AUDIT_RETENTION_DAYS to be set (see cmd/auditpurge and
# docs/OPEN_POINTS.md item 33; no default retention period is built in).
audit-purge:
	go run ./cmd/auditpurge

web-dev:
	cd web && npm run dev

web-build:
	cd web && npm run build

web-lint:
	cd web && npm run lint

web-i18n-check:
	cd web && npm run check:i18n

# CI Checklist item 4, "RLS / Permission Drift Audit" - see cmd/ciaudit and
# docs/DEVELOPMENT.md's "Continuous Integration" section.
ci-audit:
	go run ./cmd/ciaudit

# CI Checklist item 7, "License Header Check" - see scripts/license_headers.py.
license-check:
	python3 scripts/license_headers.py --check

license-fix:
	python3 scripts/license_headers.py --fix

dev-up:
	docker compose up -d

dev-down:
	docker compose down

# Fallback for environments where Docker registries are blocked but
# general internet access isn't (e.g. some sandboxed CI/agent
# environments) - see docs/DEVELOPMENT.md and scripts/dev-up-standalone.sh.
dev-up-standalone:
	./scripts/dev-up-standalone.sh

dev-down-standalone:
	./scripts/dev-down-standalone.sh
