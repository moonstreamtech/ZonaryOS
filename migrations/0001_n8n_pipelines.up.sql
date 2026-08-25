CREATE TABLE IF NOT EXISTS n8n_pipelines (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id UUID NOT NULL REFERENCES firms(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (length(name) > 0),
    definition JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive', 'draft')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_n8n_pipelines_firm_id ON n8n_pipelines(firm_id);
CREATE INDEX IF NOT EXISTS idx_n8n_pipelines_status ON n8n_pipelines(status);
CREATE INDEX IF NOT EXISTS idx_n8n_pipelines_name ON n8n_pipelines USING gin(name gin_trgm_ops);

ALTER TABLE n8n_pipelines ENABLE ROW LEVEL SECURITY;

CREATE POLICY n8n_pipelines_firm_isolation ON n8n_pipelines
    FOR ALL
    USING (firm_id = current_setting('app.firm_id')::uuid);

CREATE POLICY n8n_pipelines_owner_write ON n8n_pipelines
    FOR INSERT
    WITH CHECK (firm_id = current_setting('app.firm_id')::uuid AND current_setting('app.user_role') = 'owner');

CREATE POLICY n8n_pipelines_owner_update ON n8n_pipelines
    FOR UPDATE
    USING (firm_id = current_setting('app.firm_id')::uuid AND current_setting('app.user_role') = 'owner');

CREATE POLICY n8n_pipelines_owner_delete ON n8n_pipelines
    FOR DELETE
    USING (firm_id = current_setting('app.firm_id')::uuid AND current_setting('app.user_role') = 'owner');

CREATE POLICY n8n_pipelines_member_read ON n8n_pipelines
    FOR SELECT
    USING (firm_id = current_setting('app.firm_id')::uuid AND current_setting('app.user_role') IN ('owner', 'member'));

CREATE TRIGGER update_n8n_pipelines_updated_at
    BEFORE UPDATE ON n8n_pipelines
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();
