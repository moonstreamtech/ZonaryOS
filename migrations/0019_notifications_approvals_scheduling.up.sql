-- Notification/inbox system, approval workflow (closes Open Points item
-- 41's "no approval queue/UI" gap - see internal/workflow/rules.go's
-- EvaluateRules, which until now only ever wrote a pending_approval
-- audit_log entry with nowhere to actually review/run it), and partial
-- schedule-trigger support for the rule engine (item 7's remaining
-- "(2) Schedule-triggered rules... remain deliberately unmodeled" gap).
--
-- Tenant model: all three new tables are firm-scoped, mandatory RLS
-- (Rule 3), same convention as every other migration in this repo.

-- Schedule-triggered rules (partial - see internal/workflow/rules.go's
-- own doc comment on TriggerSchedule for the deliberate scope boundary:
-- no condition tree, notify-only actions, today). schedule_interval is a
-- Go time.Duration string (e.g. "1h", "24h"), required only when
-- trigger = 'schedule'.
ALTER TABLE workflow_rules ADD COLUMN schedule_interval text;
ALTER TABLE workflow_rules DROP CONSTRAINT workflow_rules_trigger_check;
ALTER TABLE workflow_rules ADD CONSTRAINT workflow_rules_trigger_check
    CHECK (trigger IN ('on_transition', 'on_create', 'schedule'));

CREATE TABLE notifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    recipient_user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type text NOT NULL,
    title text NOT NULL,
    body text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    read_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- pending_approvals deliberately carries one column beyond the design
-- brief's own literal schema: permission_key. Recorded once, at creation
-- time (the permission EvaluateRules resolved the proposed transition
-- action against when the rule fired), rather than re-derived from the
-- instance's CURRENT state at list/approve time - the instance's state
-- can legitimately move between request and resolution (another actor,
-- another rule), and re-deriving would either go stale silently or
-- require re-walking workflow_transitions on every list call. Approve
-- still re-validates for real via the actual ExecuteTransition call (see
-- internal/workflow/approvals.go's Approve), so this column is what gates
-- *visibility/who's allowed to try*, never the final authorization word.
CREATE TABLE pending_approvals (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    instance_id uuid NOT NULL REFERENCES workflow_instances (id) ON DELETE CASCADE,
    proposed_action_key text NOT NULL,
    permission_key text NOT NULL REFERENCES permissions (key) ON DELETE RESTRICT,
    requested_by uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    rule_id uuid REFERENCES workflow_rules (id) ON DELETE SET NULL,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'rejected', 'expired')),
    resolved_by uuid REFERENCES users (id) ON DELETE SET NULL,
    resolved_at timestamptz,
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE scheduled_rule_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    rule_id uuid NOT NULL REFERENCES workflow_rules (id) ON DELETE CASCADE,
    scheduled_for timestamptz NOT NULL,
    ran_at timestamptz,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'ran', 'failed')),
    error_text text
);

CREATE INDEX notifications_firm_id_idx ON notifications (firm_id);
-- The lookup ListForUser/UnreadCount actually perform: one recipient's
-- own notifications within one firm, unread-first.
CREATE INDEX notifications_recipient_idx ON notifications (firm_id, recipient_user_id, created_at DESC);
CREATE INDEX notifications_recipient_unread_idx ON notifications (firm_id, recipient_user_id) WHERE read_at IS NULL;

CREATE INDEX pending_approvals_firm_id_idx ON pending_approvals (firm_id);
CREATE INDEX pending_approvals_instance_id_idx ON pending_approvals (instance_id);
-- The lookup ListPendingApprovals performs: every still-pending approval
-- for one firm.
CREATE INDEX pending_approvals_pending_idx ON pending_approvals (firm_id) WHERE status = 'pending';

CREATE INDEX scheduled_rule_runs_firm_id_idx ON scheduled_rule_runs (firm_id);
CREATE INDEX scheduled_rule_runs_rule_id_idx ON scheduled_rule_runs (rule_id);
-- The lookup the background scheduler goroutine actually performs every
-- minute: every still-pending, due run for one firm.
CREATE INDEX scheduled_rule_runs_due_idx ON scheduled_rule_runs (firm_id, scheduled_for) WHERE status = 'pending';

GRANT SELECT, INSERT, UPDATE ON notifications TO zonaryos_app;
GRANT SELECT, INSERT, UPDATE ON pending_approvals TO zonaryos_app;
GRANT SELECT, INSERT, UPDATE ON scheduled_rule_runs TO zonaryos_app;

ALTER TABLE notifications ENABLE ROW LEVEL SECURITY;
ALTER TABLE pending_approvals ENABLE ROW LEVEL SECURITY;
ALTER TABLE scheduled_rule_runs ENABLE ROW LEVEL SECURITY;

-- Tenant isolation only (Rule 3) - "a user sees only their own
-- notifications" is an application-level filter (WHERE recipient_user_id
-- = $callerUserID) on top of this, the same layering every other
-- firm-scoped table in this codebase uses for a *finer* restriction than
-- RLS itself expresses: RLS's own app.current_firm_id session setting has
-- no per-request "current acting user" equivalent outside the
-- WithUserContext/WithInviteTokenContext bootstrap carve-outs (see
-- internal/platform/db.tenant.go), so recipient scoping happens in Go,
-- the same way IsMember/Has already gate every other read in this
-- codebase beyond RLS's own firm-only guarantee.
CREATE POLICY notifications_tenant_isolation ON notifications
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY pending_approvals_tenant_isolation ON pending_approvals
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY scheduled_rule_runs_tenant_isolation ON scheduled_rule_runs
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
