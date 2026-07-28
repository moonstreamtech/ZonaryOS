DROP POLICY user_firm_roles_tenant_isolation ON user_firm_roles;

CREATE POLICY user_firm_roles_tenant_isolation ON user_firm_roles
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

DROP FUNCTION IF EXISTS app_current_user_id();
