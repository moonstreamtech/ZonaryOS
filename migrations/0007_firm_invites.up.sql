-- Open Points item 17's companion gap ("no way to get a second real user
-- into a firm") - deliberately NOT the same thing as item 17 itself. Item
-- 17 (email integration) stays fully deferred: this migration adds no
-- SMTP/email-sending infrastructure of any kind. Instead it's a shareable
-- link an owner copies out of the UI and hands to someone by whatever
-- channel they already use (Slack, a text message, in person) - the
-- invite is the opaque token below, nothing more.
--
-- Tenant-scoped like every other per-firm table (Never-Violate Rule 3),
-- with one deliberate carve-out on the *read* side only, mirroring
-- migrations/0002_user_scoped_discovery.up.sql's own reasoning: the
-- accept-flow has to look up a token before the person accepting it has
-- any firm context at all (they are not a member of the firm yet - that
-- is the entire point of accepting an invite). Widening the read policy
-- to also allow "the row whose token matches the token this transaction
-- was scoped to" (app_current_invite_token(), set via
-- internal/platform/db.WithInviteTokenContext, the same SET LOCAL
-- session-setting pattern app_current_user_id() already established) is
-- the honest way to express "this token is the caller's own credential to
-- look up" - it is unguessable (32 random bytes, see
-- internal/invite.generateToken) so knowing it is equivalent to being
-- authorized to read that one row, the same trust model a password-reset
-- link or an unlisted-but-unguessable share link uses elsewhere on the
-- web. WITH CHECK is NOT widened the same way: every INSERT/UPDATE still
-- requires an established firm_id context (WithFirmContext), so a token
-- alone can mark itself used only from within a transaction that has
-- already resolved and set that firm's context first - see
-- internal/invite.Accept for exactly that sequencing.
CREATE TABLE firm_invites (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    token text NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'used', 'revoked')),
    expires_at timestamptz NOT NULL,
    created_by_user_id uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    used_at timestamptz,
    used_by_user_id uuid REFERENCES users (id) ON DELETE RESTRICT
);

CREATE INDEX firm_invites_firm_id_idx ON firm_invites (firm_id);

-- Looked up by token alone on the accept path (internal/invite.Lookup/
-- Accept), before any firm_id is known - a plain UNIQUE constraint above
-- already gives this an index, but naming it explicitly here documents
-- that this is a deliberate, load-bearing lookup path, not an
-- afterthought.
CREATE UNIQUE INDEX firm_invites_token_idx ON firm_invites (token);

-- No DELETE grant, matching this codebase's general soft-state preference
-- where it already applies (e.g. audit_log itself is never hard-deleted
-- outside cmd/auditpurge's retention job) - a revoked invite stays in the
-- table as its own audit trail of what was revoked and by/when
-- (internal/invite.Revoke sets status='revoked', never removes the row).
GRANT SELECT, INSERT, UPDATE ON firm_invites TO zonaryos_app;

ALTER TABLE firm_invites ENABLE ROW LEVEL SECURITY;

-- Mirrors app_current_user_id() (migrations/0002_user_scoped_discovery.up.sql):
-- resolves the invite token bound to the current transaction, or NULL if
-- unset/empty - same NULLIF(...,'')::text collapse reasoning as that
-- function's own comment (a pooled connection's post-COMMIT reset value
-- for a custom GUC is '', not NULL, so this fails closed instead of
-- accidentally matching a row whose token happens to be '').
CREATE FUNCTION app_current_invite_token() RETURNS text
    LANGUAGE sql STABLE AS $$
    SELECT NULLIF(current_setting('app.current_invite_token', true), '');
$$;

CREATE POLICY firm_invites_tenant_isolation ON firm_invites
    USING (firm_id = app_current_firm_id() OR token = app_current_invite_token())
    WITH CHECK (firm_id = app_current_firm_id());
