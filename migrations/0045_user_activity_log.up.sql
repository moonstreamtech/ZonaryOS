-- User activity dashboard widget (Issue #59): a firm-scoped log of recent
-- user actions, read via GET .../activity (paginated, filterable by
-- user/event type). Distinct from `audit_log` (migrations/0003) - that
-- table is Vision §3's compliance-grade audit trail (gated behind the
-- audit.log.read permission, retained per docs/OPEN_POINTS.md item 33);
-- this one is a lightweight, unprivileged "what have I/we been doing"
-- feed any firm member can read about their own firm, the same tier as
-- internal/notification's own inbox rows. Mandatory RLS (Rule 3), same
-- convention as every other migration here.

CREATE TABLE user_activity_log (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    event_type text NOT NULL,
    event_data jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX user_activity_log_firm_id_idx ON user_activity_log (firm_id);
CREATE INDEX user_activity_log_firm_created_idx ON user_activity_log (firm_id, created_at DESC);
CREATE INDEX user_activity_log_user_id_idx ON user_activity_log (user_id);

GRANT SELECT, INSERT ON user_activity_log TO zonaryos_app;

ALTER TABLE user_activity_log ENABLE ROW LEVEL SECURITY;

CREATE POLICY user_activity_log_tenant_isolation ON user_activity_log
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
