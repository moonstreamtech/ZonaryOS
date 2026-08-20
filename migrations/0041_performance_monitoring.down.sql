REVOKE CREATE ON SCHEMA public FROM zonaryos_app;
REVOKE pg_monitor FROM zonaryos_app;

DROP TABLE IF EXISTS alert_events;
DROP TABLE IF EXISTS alert_rules;
DROP TABLE IF EXISTS request_metrics;
