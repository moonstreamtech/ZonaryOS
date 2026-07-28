# Development Setup

This document covers local setup for the ZonaryOS application (backend + frontend). For product scope and architecture decisions, see `docs/VISION.md`. For working rules, see `CLAUDE.md`.

## Prerequisites

- Go 1.24+
- Node.js 22+
- Docker (for Postgres and Keycloak)

## Backend

```
cp .env.example .env   # adjust as needed
make build
make test
make run                # starts the server on :8080
```

`GET /healthz` returns `{"status":"ok"}` once the server is up.

## Frontend

```
cd web
cp .env.example .env.local
npm install
npm run dev              # starts the frontend on :3000
```

The frontend serves locale-prefixed routes (`/en`, `/tr`) via `next-intl`; `/` redirects to the default locale. All UI copy comes from `web/messages/{locale}.json` — no hardcoded strings (see `CLAUDE.md`, Never-Violate Rule 4).

## Local infrastructure (Postgres, Keycloak)

```
make dev-up      # starts Postgres (:5432) and Keycloak (:8081) via docker-compose
make dev-down     # stops them
```

Postgres and Keycloak wiring (migrations, RLS policies, realm configuration) land in subsequent PRs as the vertical slice is built out.
