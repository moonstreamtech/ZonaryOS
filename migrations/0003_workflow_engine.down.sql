REVOKE INSERT ON permissions FROM zonaryos_app;

DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS workflow_instances;
DROP TABLE IF EXISTS workflow_transitions;
DROP TABLE IF EXISTS workflow_states;
DROP TABLE IF EXISTS workflow_definitions;
