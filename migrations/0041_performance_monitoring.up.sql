-- Performance monitoring, alerting, and operational excellence batch.
--
-- Part 1: request_metrics. Real, working declarative partitioning by
-- month (PARTITION BY RANGE (occurred_at)), same "not a comment
-- promising it later" bar migrations/0040's analytics_events sets - but
-- unlike that table (which ships with only a DEFAULT catch-all, since it
-- had no operational partition-maintenance story yet), this batch's own
-- Part 4 self-healing scheduler (internal/apm.RunSelfHealingScheduler)
-- exists specifically to keep this table's partitions ahead of the
-- data, so this migration creates three real, explicitly-bounded month
-- partitions up front (the month this migration was authored, plus the
-- two after it) rather than relying on the DEFAULT partition to catch
-- everything indefinitely. The DEFAULT partition still exists as a
-- safety net for any row that lands outside those three months (e.g. an
-- installation that goes a long time between deploys before the
-- self-healing scheduler gets a chance to run) - it is never the
-- primary home for current data, only a backstop.
--
-- Partition-naming convention: request_metrics_YYYY_MM (zero-padded
-- month, e.g. request_metrics_2026_08) - a fixed, sortable, collision-
-- free name any future partition (created by this migration or by the
-- self-healing scheduler at runtime) can be derived from occurred_at
-- alone with no lookup table, and one a human reading `\d+ request_metrics`
-- can immediately map back to a calendar month.
--
-- The request_firm_id column (deliberately NOT named firm_id) records
-- which firm's own request this was, when known (nullable - an
-- unauthenticated request, e.g. GET /health or GET /api/version, has
-- none) - but it is metadata about the request, not a tenant-isolation
-- boundary: this table is platform-level operational telemetry read
-- exclusively by platform-admin staff across every firm at once (the
-- entire point of GET /api/platform-admin/metrics/summary is a
-- cross-firm view), never firm-scoped the way analytics_events/
-- scheduled_reports are. Naming it request_firm_id rather than firm_id
-- is the same deliberate choice migrations/0033_multi_company_consolidation's
-- own comment on member_firm_id/from_firm_id/to_firm_id documents:
-- cmd/ciaudit's RLS-required check matches the literal word firm_id, so
-- a column that references firms.id but is NOT meant to be a per-row
-- RLS isolation key is named to say so - not RLS-exempted through an
-- accident of naming, but through an honest, precedented naming choice
-- that makes the exemption visible to that same audit tool's reviewer.
CREATE TABLE request_metrics (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    method text NOT NULL,
    path_pattern text NOT NULL,
    status_code integer NOT NULL,
    duration_ms integer NOT NULL,
    request_firm_id uuid REFERENCES firms (id) ON DELETE SET NULL,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, occurred_at)
) PARTITION BY RANGE (occurred_at);

CREATE TABLE request_metrics_2026_08 PARTITION OF request_metrics
    FOR VALUES FROM ('2026-08-01') TO ('2026-09-01');
CREATE TABLE request_metrics_2026_09 PARTITION OF request_metrics
    FOR VALUES FROM ('2026-09-01') TO ('2026-10-01');
CREATE TABLE request_metrics_2026_10 PARTITION OF request_metrics
    FOR VALUES FROM ('2026-10-01') TO ('2026-11-01');
CREATE TABLE request_metrics_default PARTITION OF request_metrics DEFAULT;

CREATE INDEX request_metrics_occurred_at_idx ON request_metrics (occurred_at);
CREATE INDEX request_metrics_path_pattern_occurred_at_idx ON request_metrics (path_pattern, occurred_at);
CREATE INDEX request_metrics_status_code_occurred_at_idx ON request_metrics (status_code, occurred_at);

-- Append-only from the app's own perspective, same "no UPDATE/DELETE
-- grant" posture analytics_events already establishes for its own
-- immutable log - a recorded request metric is never revised in place.
GRANT SELECT, INSERT ON request_metrics TO zonaryos_app;

-- Part 4's own self-healing partition-creation scheduler
-- (internal/apm.EnsureFuturePartitions) runs CREATE TABLE ... PARTITION
-- OF request_metrics as the ordinary application role (zonaryos_app),
-- not the privileged migration role - every other migration in this
-- schema only ever creates tables at migrate time, as the owner/
-- superuser role (internal/platform/db.Migrate), so this is the first
-- table whose own DDL needs to keep happening at runtime, by the app
-- role. CREATE TABLE requires CREATE privilege on the containing schema
-- itself (a table-level GRANT, like the one just above, is not enough
-- for DDL) - this is the narrowest possible grant for that (schema-level
-- CREATE only, nothing database-wide, and no privilege over any object
-- this role doesn't already reach some other way).
GRANT CREATE ON SCHEMA public TO zonaryos_app;

-- Part 2: alerting. Platform-level (no firm_id/request_firm_id column
-- at all - an alert rule like "error rate above 5% in the last hour" is
-- a statement about the installation as a whole, not about any one
-- firm's own data), so - like exchange_rates
-- (migrations/0020_validation_currency_localization.up.sql) - neither
-- table here needs RLS: cmd/ciaudit's RLS check only ever fires on a
-- table that actually has a firm_id column, and correctly does not fire
-- on either of these.
CREATE TABLE alert_rules (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    -- Open, small vocabulary - 'error_rate'/'response_time_p95'/
    -- 'volume_drop' are this batch's own three illustrative metrics
    -- (internal/alerting.Checker's entire switch today), not a closed
    -- DB CHECK: a future metric is a new Go case, not a schema change.
    metric text NOT NULL,
    threshold numeric NOT NULL,
    window_minutes integer NOT NULL DEFAULT 60 CHECK (window_minutes > 0),
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE alert_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id uuid NOT NULL REFERENCES alert_rules (id) ON DELETE CASCADE,
    triggered_at timestamptz NOT NULL DEFAULT now(),
    metric_value numeric NOT NULL,
    resolved_at timestamptz
);

CREATE INDEX alert_rules_is_active_idx ON alert_rules (is_active);
CREATE INDEX alert_events_rule_id_triggered_at_idx ON alert_events (rule_id, triggered_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON alert_rules TO zonaryos_app;
-- Append-only-plus-resolve: an alert_events row's own resolved_at is
-- the one legitimate in-place update (Part 2's own "resolved" lifecycle
-- - this batch's checker only ever inserts new events, never resolves
-- one automatically, but the column and this grant exist for a future
-- "acknowledge/resolve" endpoint that isn't otherwise blocked on a
-- schema change) - metric_value/triggered_at/rule_id are otherwise
-- immutable once recorded.
GRANT SELECT, INSERT, UPDATE ON alert_events TO zonaryos_app;

-- Part 3: database health monitoring (GET /health?detail=true) needs to
-- read OTHER backends' own pg_stat_activity.query/query_start columns
-- (Postgres hides those two columns for any backend that isn't the
-- current user, unless the caller holds pg_monitor or is a superuser) -
-- zonaryos_app is neither, so it needs this built-in role explicitly.
-- pg_monitor is read-only monitoring-view access only (it grants no
-- write capability whatsoever, on this table or any other) - the
-- narrowest built-in role that satisfies this requirement.
GRANT pg_monitor TO zonaryos_app;
