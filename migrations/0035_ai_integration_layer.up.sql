-- AI integration layer + intelligent automation (Vision §5: "sistemi
-- değiştiren değil sistemi öğreten yardımcı katman" - a helper layer
-- that teaches the system, not one that changes it on its own).
-- Provider-agnostic AI configuration per firm, plus the schema this
-- batch's suggestion/anomaly-detection features build on top of it.
--
-- Tenant model: firm-scoped, mandatory RLS (Rule 3), same convention as
-- every other migration in this repo.

CREATE TABLE ai_configurations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider IN ('anthropic', 'openai', 'google', 'custom')),
    model text NOT NULL,
    -- AES-256-GCM ciphertext (nonce || ciphertext || auth tag, all
    -- base64-encoded as one string) - see internal/ai/crypto.go's own
    -- doc comment for why this is encryption, not hashing: the plaintext
    -- API key must be recoverable to actually call the provider, unlike
    -- e.g. edge_agent_tokens.token_hash, which only ever needs an
    -- equality check.
    api_key_encrypted text NOT NULL,
    settings jsonb NOT NULL DEFAULT '{}'::jsonb,
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ai_configurations_firm_id_idx ON ai_configurations (firm_id);
-- Single-active-configuration-per-firm enforcement (internal/ai's own
-- deactivateOtherActiveConfigsTx) queries exactly this shape - same
-- "flip the others in the same transaction" application-level pattern
-- internal/manufacturing's BOM active-version enforcement and
-- internal/budget's single-active-budget enforcement already establish,
-- not a DB constraint.
CREATE INDEX ai_configurations_active_idx ON ai_configurations (firm_id) WHERE is_active;

GRANT SELECT, INSERT, UPDATE, DELETE ON ai_configurations TO zonaryos_app;

ALTER TABLE ai_configurations ENABLE ROW LEVEL SECURITY;

CREATE POLICY ai_configurations_tenant_isolation ON ai_configurations
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
