-- User experience improvements + onboarding flow + help system batch:
-- first-run onboarding progress tracking, a contextual help article
-- library, and per-user display preferences.
--
-- Tenant model:
--   - user_onboarding_progress is firm-scoped (a user's onboarding
--     checklist is tracked per firm, since the steps themselves - "add
--     your first product", "run your first report" - are firm actions):
--     mandatory RLS (Rule 3), same convention as every other migration in
--     this repo. Application code additionally filters by user_id
--     (defense in depth alongside RLS, same pattern
--     internal/inventory.GetProduct's own id+firm_id WHERE clause
--     establishes) since one firm can have many members each with their
--     own onboarding progress - the RLS policy alone only proves "this
--     row belongs to a firm you're scoped into", not "this row is yours".
--   - help_articles is global product documentation, not owned by any
--     firm - no firm_id column, same precedent the `users` table itself
--     established (migrations/0001_core_schema.up.sql's own "not
--     RLS-scoped: a user's own identity record isn't owned by any single
--     firm" reasoning applies equally here). Exempt from Rule 3's RLS
--     mandate for the same reason `users` is.
--   - user_preferences is likewise global/user-scoped, not firm-scoped -
--     a user's theme/density/locale preference follows them across every
--     firm they belong to, the same "global identity" tier as `users`
--     and, now, help_articles.
--
-- Scope boundary: no guided product tour with tooltips overlay (just the
-- onboarding checklist), no in-app video tutorials, no live chat support
-- widget, no feedback collection form.

CREATE TABLE user_onboarding_progress (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    completed_steps text[] NOT NULL DEFAULT '{}',
    dismissed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, user_id)
);

CREATE INDEX user_onboarding_progress_firm_id_idx ON user_onboarding_progress (firm_id);
CREATE INDEX user_onboarding_progress_user_id_idx ON user_onboarding_progress (user_id);

GRANT SELECT, INSERT, UPDATE ON user_onboarding_progress TO zonaryos_app;

ALTER TABLE user_onboarding_progress ENABLE ROW LEVEL SECURITY;

CREATE POLICY user_onboarding_progress_tenant_isolation ON user_onboarding_progress
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

-- help_articles: global content, Markdown-formatted, title/content
-- carried in both shipped locales (en mandatory, tr mandatory per the
-- design brief's seed data; ar is deliberately left out of this table's
-- NOT NULL columns - see internal/helparticles's own doc comment for why
-- a per-locale fallback-to-en read path exists instead of a third
-- mandatory column nothing populates yet).
CREATE TABLE help_articles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug text NOT NULL UNIQUE,
    title_en text NOT NULL,
    title_tr text NOT NULL,
    content_en text NOT NULL,
    content_tr text NOT NULL,
    related_route text,
    search_tsv tsvector,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Same 'simple'-config, trigger-maintained tsvector pattern
-- migrations/0024_fulltext_search_filtering.up.sql establishes (see that
-- migration's own doc comment for why 'simple' + normalize_search_text,
-- never a language-specific config or websearch_to_tsquery).
CREATE OR REPLACE FUNCTION help_articles_search_tsv_update() RETURNS trigger AS $$
BEGIN
    NEW.search_tsv := to_tsvector('simple', normalize_search_text(
        COALESCE(NEW.title_en, '') || ' ' || COALESCE(NEW.title_tr, '') || ' ' ||
        COALESCE(NEW.content_en, '') || ' ' || COALESCE(NEW.content_tr, '')
    ));
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER help_articles_search_tsv_trigger
    BEFORE INSERT OR UPDATE ON help_articles
    FOR EACH ROW EXECUTE FUNCTION help_articles_search_tsv_update();

CREATE INDEX help_articles_search_tsv_idx ON help_articles USING GIN (search_tsv);
CREATE INDEX help_articles_related_route_idx ON help_articles (related_route);

-- Read-only reference content from the application's point of view (no
-- POST/PATCH/DELETE endpoint - see internal/helparticles's own doc
-- comment): zonaryos_app only ever SELECTs. Authoring happens by
-- shipping a new migration, the same append-only-content precedent
-- other seed/reference tables in this repo already use.
GRANT SELECT ON help_articles TO zonaryos_app;

-- user_preferences: no firm_id, not RLS-scoped - same global-identity
-- tier as `users` (migrations/0001_core_schema.up.sql). Scoped by
-- user_id = <server-resolved userID> in application code, never a
-- client-supplied value - the same trust model `users` itself relies on.
CREATE TABLE user_preferences (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    preferences jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT, UPDATE ON user_preferences TO zonaryos_app;
