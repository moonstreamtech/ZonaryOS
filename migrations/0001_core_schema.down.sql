DROP FUNCTION IF EXISTS app_current_firm_id();

DROP TABLE IF EXISTS user_firm_roles;
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS permissions;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS firms;

REVOKE ALL ON SCHEMA public FROM zonaryos_app;
DROP ROLE IF EXISTS zonaryos_app;
