-- Lets a freshly-authenticated user discover which firm(s) they belong to
-- before any firm context has been established - the chicken-and-egg gap
-- in the original user_firm_roles policy (migrations/0001): reading that
-- table required a firm_id already, but the user doesn't know which firm
-- to scope to until they've read their own membership list.
--
-- Resolved by widening the *read* side of the policy only: a row is also
-- visible when it belongs to the current authenticated user, regardless of
-- firm. Writes are unaffected - WITH CHECK still requires an established
-- firm context, so assigning a user to a firm still has to happen inside
-- a WithFirmContext transaction for that firm, never just because it's
-- "your own" row.

CREATE FUNCTION app_current_user_id() RETURNS uuid
    LANGUAGE sql STABLE AS $$
    SELECT NULLIF(current_setting('app.current_user_id', true), '')::uuid;
$$;

DROP POLICY user_firm_roles_tenant_isolation ON user_firm_roles;

CREATE POLICY user_firm_roles_tenant_isolation ON user_firm_roles
    USING (firm_id = app_current_firm_id() OR user_id = app_current_user_id())
    WITH CHECK (firm_id = app_current_firm_id());
