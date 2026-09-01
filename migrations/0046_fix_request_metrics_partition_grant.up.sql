-- Fixes a bug in migrations/0041_performance_monitoring.up.sql: that
-- migration's own comment reasoned that `GRANT CREATE ON SCHEMA public`
-- would let zonaryos_app run `CREATE TABLE ... PARTITION OF
-- request_metrics` at runtime (internal/apm.EnsureFuturePartitions). It
-- does not - PostgreSQL requires ownership of the PARENT table to
-- attach a partition to it; schema-level CREATE only lets a role create
-- new, independent objects in that schema. request_metrics is owned by
-- the migration/admin role (internal/platform/db.Migrate), not
-- zonaryos_app, so every attach attempt by the app role has always
-- failed with "must be owner of table request_metrics" (42501). This
-- was dormant for 11 days (2026-08-20 to 2026-09-01) purely because
-- 0041's own static Aug/Sep/Oct 2026 partitions happened to cover every
-- CI run's 3-month look-ahead window until the calendar rolled past
-- October - see docs/DEVELOPMENT.md's "request_metrics partition
-- creation" section for the full incident writeup.
--
-- Fix: a SECURITY DEFINER function, owned by the migration/admin role,
-- that does exactly one narrow thing - create one request_metrics
-- partition for one calendar month, with the month's shape validated
-- inside the function itself. zonaryos_app gets EXECUTE on this
-- function instead of CREATE on the schema: it can create a
-- request_metrics partition and nothing else it couldn't already do
-- (no ability to create arbitrary objects in public, no ownership of
-- request_metrics itself - see docs/DEVELOPMENT.md for the full
-- comparison against the alternatives considered: ALTER TABLE ... OWNER
-- TO zonaryos_app, running partition maintenance on an admin
-- connection, and pre-creating partitions further ahead in a
-- migration).
--
-- Bounded on purpose: EXECUTE on a table-creating function is only as
-- safe as the function's own limits. month_start must be the first of
-- a month (rejects a misaligned range that would silently block the
-- correct partition from ever being created later - CREATE TABLE IF
-- NOT EXISTS only checks the table NAME, not whether its range makes
-- sense) and must fall within 1 month back to 6 months ahead of
-- current_date (internal/apm's own look-ahead window is 3 months, so
-- this leaves headroom without being open-ended - zonaryos_app cannot
-- use this grant to create an unbounded number of tables).
--
-- Does NOT attempt to auto-remediate a request_metrics_default
-- conflict (PostgreSQL refuses to attach a partition if the DEFAULT
-- partition already holds rows that belong in the new partition's
-- range - verified empirically against a real PostgreSQL 16 instance:
-- SQLSTATE 23514, check_violation, "updated partition constraint for
-- default partition ... would be violated by some row" - this is
-- exactly the failure mode 11 days of this bug created for
-- 2026-11: if the scheduler was down for a month, that month's rows
-- are already sitting in the default partition by the time this fix
-- lands). Moving rows out of the default partition is a data
-- operation, not something a partition-creation function should do
-- silently, so this re-raises with an actionable message instead and
-- lets the self-healing scheduler's existing slog.Error posture
-- (internal/apm/selfheal.go's ProcessSelfHealing) surface it loudly,
-- repeatedly, every hour, until a human fixes it by hand.
CREATE FUNCTION create_request_metrics_partition(month_start date)
    RETURNS void
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = public, pg_temp
    AS $$
DECLARE
    partition_name text;
BEGIN
    IF month_start <> date_trunc('month', month_start)::date THEN
        RAISE EXCEPTION 'create_request_metrics_partition: % is not the first of a month', month_start;
    END IF;
    IF month_start > current_date + interval '6 months'
        OR month_start < current_date - interval '1 month' THEN
        RAISE EXCEPTION 'create_request_metrics_partition: % is outside the allowed window (1 month back to 6 months ahead of %)', month_start, current_date;
    END IF;

    partition_name := 'request_metrics_' || to_char(month_start, 'YYYY_MM');

    BEGIN
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF request_metrics FOR VALUES FROM (%L) TO (%L)',
            partition_name, month_start, month_start + interval '1 month'
        );
    EXCEPTION WHEN check_violation THEN
        -- SQLSTATE 23514 - request_metrics_default already holds a row
        -- that belongs in this month's range. Recovery is an admin
        -- operation (move those rows into partition_name by hand, or
        -- have the admin role attach the partition directly - it owns
        -- request_metrics and is not subject to this function's own
        -- bound, see docs/DEVELOPMENT.md), never automatic here.
        RAISE EXCEPTION 'create_request_metrics_partition: cannot attach % - request_metrics_default already contains rows in this range (the scheduler was likely down for this interval). Move those rows into % by hand (as the admin role), then re-run.', partition_name, partition_name;
    END;
END;
$$;

-- SECURITY DEFINER functions default to callable by PUBLIC (any role in
-- the database) unless revoked - explicit revoke-then-grant here keeps
-- this exactly as narrow as the rest of this schema's grants, rather
-- than relying on "there happen to be no other roles today."
REVOKE EXECUTE ON FUNCTION create_request_metrics_partition(date) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION create_request_metrics_partition(date) TO zonaryos_app;

-- The schema-wide grant 0041 added for this purpose never actually
-- worked for it (see this migration's own header comment) and is
-- broader than zonaryos_app needs for anything else in this schema -
-- removing it is a net narrowing, not a lateral move.
REVOKE CREATE ON SCHEMA public FROM zonaryos_app;
