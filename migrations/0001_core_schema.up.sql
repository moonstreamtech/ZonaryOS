-- Core identity/tenant schema for the first vertical slice.
--
-- Tenant model: `firms` and `users` are global tables (a user can belong to
-- several firms, per user_firm_roles below - see the many-to-many decision
-- for this slice). Every other table here is tenant-scoped by `firm_id` and
-- carries a mandatory Row-Level Security policy (Never-Violate Rule 3):
-- isolation is enforced by Postgres itself, never by application-level
-- filtering alone.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Tenant registry. Not RLS-scoped itself: there is no broader tenant above a firm.
CREATE TABLE firms (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    attributes jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Global identity table, keyed off the Keycloak subject. Not RLS-scoped:
-- a user's own identity record isn't owned by any single firm.
CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    keycloak_subject text NOT NULL UNIQUE,
    email text NOT NULL,
    display_name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Global permission catalog, owned/seeded by ZonaryOS itself (system-defined
-- keys such as "inventory.product.create"), not per-tenant data.
CREATE TABLE permissions (
    key text PRIMARY KEY,
    description text NOT NULL
);

-- Tenant-scoped: a firm's own custom roles.
CREATE TABLE roles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    key text NOT NULL,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, key)
);

-- Tenant-scoped: which permissions a firm's role grants.
CREATE TABLE role_permissions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    permission_key text NOT NULL REFERENCES permissions (key) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (role_id, permission_key)
);

-- Tenant-scoped many-to-many: a user can belong to several firms, with a
-- role per (user, firm) pairing. Chosen deliberately over a single firm_id
-- on `users` so this never needs a retrofit migration later.
CREATE TABLE user_firm_roles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, firm_id, role_id)
);

CREATE INDEX roles_firm_id_idx ON roles (firm_id);
CREATE INDEX role_permissions_firm_id_idx ON role_permissions (firm_id);
CREATE INDEX user_firm_roles_firm_id_idx ON user_firm_roles (firm_id);
CREATE INDEX user_firm_roles_user_id_idx ON user_firm_roles (user_id);

-- Application runtime role. It is deliberately NOT a superuser and NOT the
-- owner of these tables: RLS policies are enforced against table owners'
-- bypass privilege, so the app must connect as a distinct, unprivileged
-- role for the policies below to have any effect at all.
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'zonaryos_app') THEN
        CREATE ROLE zonaryos_app LOGIN PASSWORD 'zonaryos_app';
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO zonaryos_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON firms, users, roles, role_permissions, user_firm_roles TO zonaryos_app;
GRANT SELECT ON permissions TO zonaryos_app;

ALTER TABLE roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE role_permissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_firm_roles ENABLE ROW LEVEL SECURITY;

-- Resolves the firm bound to the current transaction, or NULL if unset.
--
-- A plain `current_setting('app.current_firm_id', true)` is not enough:
-- for a custom (placeholder) GUC, once ANY transaction on a given pooled
-- connection has SET LOCAL'd it, Postgres's reset value after COMMIT is an
-- empty string, not NULL - connection pooling means later, unrelated
-- transactions on that same physical connection would otherwise see ''
-- instead of "unset", which fails the ::uuid cast instead of failing
-- closed. NULLIF(..., '') collapses that empty string back to NULL first.
CREATE FUNCTION app_current_firm_id() RETURNS uuid
    LANGUAGE sql STABLE AS $$
    SELECT NULLIF(current_setting('app.current_firm_id', true), '')::uuid;
$$;

-- Isolation policy: a row is only visible/writable when its firm_id matches
-- the firm bound to the current transaction (see internal/platform/db
-- WithFirmContext, which sets this via `SET LOCAL` on every request).
-- firm_id = NULL is always false - so an un-scoped connection sees nothing,
-- which is the correct fail-closed default.
CREATE POLICY roles_tenant_isolation ON roles
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY role_permissions_tenant_isolation ON role_permissions
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY user_firm_roles_tenant_isolation ON user_firm_roles
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
