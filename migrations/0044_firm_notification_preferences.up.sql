-- Per-firm notification preferences: lets a firm turn off in-app/email
-- notifications for a given event type (invoice sent, payment received,
-- ...) firm-wide. Firm-scoped, not per-user (no user_id column) - this is
-- a firm setting, the same tier as internal/firm's own settings, distinct
-- from internal/notification's per-recipient inbox rows (migrations/0019).
-- Mandatory RLS (Rule 3), same convention as every other migration here.

CREATE TABLE firm_notification_preferences (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    notification_type text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, notification_type)
);

CREATE INDEX firm_notification_preferences_firm_id_idx ON firm_notification_preferences (firm_id);

GRANT SELECT, INSERT, UPDATE ON firm_notification_preferences TO zonaryos_app;

ALTER TABLE firm_notification_preferences ENABLE ROW LEVEL SECURITY;

CREATE POLICY firm_notification_preferences_tenant_isolation ON firm_notification_preferences
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
