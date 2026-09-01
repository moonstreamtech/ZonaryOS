-- Reversibility, not endorsement: restoring GRANT CREATE ON SCHEMA
-- public TO zonaryos_app puts back a grant this migration's own .up.sql
-- header establishes is both ineffective for its original purpose
-- (attaching a request_metrics partition needs ownership of the parent
-- table, which this grant never conferred) and broader than
-- zonaryos_app needs for anything else in this schema. This down
-- migration exists so `migrate down` round-trips cleanly to exactly
-- 0041's post-migration state, not because that state is the
-- recommended one - do not read this as "the grant is fine after all."
GRANT CREATE ON SCHEMA public TO zonaryos_app;

REVOKE EXECUTE ON FUNCTION create_request_metrics_partition(date) FROM zonaryos_app;
DROP FUNCTION IF EXISTS create_request_metrics_partition(date);
