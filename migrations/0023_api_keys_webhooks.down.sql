DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhooks;
DROP POLICY IF EXISTS api_keys_tenant_isolation ON api_keys;
DROP FUNCTION IF EXISTS app_current_api_key_hash();
DROP TABLE IF EXISTS api_keys;
