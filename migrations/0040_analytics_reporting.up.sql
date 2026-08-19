-- Analytics, business intelligence, and advanced reporting batch.
--
-- Part 1: analytics_events. Partitioning design choice: this table is
-- declared PARTITION BY RANGE (occurred_at) from the start, with a
-- single catch-all DEFAULT partition attached immediately below - real,
-- working declarative partitioning, not just a comment promising it
-- later. No time-bounded partitions (e.g. one per month) are created by
-- this migration: doing that well needs an operational retention policy
-- (how many months to keep, when to detach/drop old partitions) this
-- batch doesn't define, and creating them ahead of any real data would
-- just be guessing at boundaries. Everything lands in the DEFAULT
-- partition until a future migration/operational task attaches real
-- time-bounded partitions and moves rows into them - documented here as
-- that future concern, not solved by this batch. The partition key
-- (occurred_at) must be part of any PRIMARY KEY on a partitioned table,
-- which is why the key here is (id, occurred_at) rather than id alone.
CREATE TABLE analytics_events (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    -- Open vocabulary (not a CHECK constraint) - 'page_view'/'feature_used'/
    -- 'workflow_executed' are illustrative examples from this batch's own
    -- design brief, not an exhaustive fixed set; new event types are added
    -- by callers, not by a schema migration.
    event_type text NOT NULL,
    entity_type text,
    entity_id uuid,
    user_id uuid,
    properties jsonb NOT NULL DEFAULT '{}',
    occurred_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, occurred_at)
) PARTITION BY RANGE (occurred_at);

CREATE TABLE analytics_events_default PARTITION OF analytics_events DEFAULT;

ALTER TABLE analytics_events ENABLE ROW LEVEL SECURITY;

CREATE POLICY analytics_events_tenant_isolation ON analytics_events
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE INDEX analytics_events_firm_id_occurred_at_idx ON analytics_events (firm_id, occurred_at);
CREATE INDEX analytics_events_firm_id_event_type_idx ON analytics_events (firm_id, event_type);
CREATE INDEX analytics_events_firm_id_user_id_idx ON analytics_events (firm_id, user_id);

-- Append-only from the app's own perspective (SELECT, INSERT only) - an
-- analytics event is an immutable fact about something that already
-- happened, the same "no UPDATE/DELETE grant" posture stock_movements
-- already establishes for its own immutable log.
GRANT SELECT, INSERT ON analytics_events TO zonaryos_app;

-- Part 4: scheduled_reports. schedule_interval is a small closed
-- vocabulary (daily/weekly/monthly), a real DB CHECK constraint -
-- recipient_user_ids is a plain uuid[] (not a join table): a scheduled
-- report's recipient list is a small, report-owned set with no need for
-- its own row-level metadata, the same reasoning workflow_definitions'
-- own small denormalized arrays elsewhere in this schema already follow.
CREATE TABLE scheduled_reports (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    definition_id uuid NOT NULL REFERENCES report_definitions (id) ON DELETE CASCADE,
    name text NOT NULL,
    schedule_interval text NOT NULL
        CHECK (schedule_interval IN ('daily', 'weekly', 'monthly')),
    last_run_at timestamptz,
    next_run_at timestamptz,
    recipient_user_ids uuid[] NOT NULL DEFAULT '{}',
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE scheduled_reports ENABLE ROW LEVEL SECURITY;

CREATE POLICY scheduled_reports_tenant_isolation ON scheduled_reports
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE INDEX scheduled_reports_firm_id_idx ON scheduled_reports (firm_id);
CREATE INDEX scheduled_reports_next_run_at_idx ON scheduled_reports (next_run_at) WHERE is_active;

GRANT SELECT, INSERT, UPDATE, DELETE ON scheduled_reports TO zonaryos_app;
