-- Asset registry, maintenance scheduling, and maintenance records: physical
-- assets (equipment, vehicles, property, tools) are foundational to any
-- business, per the design brief. Not a full facilities/EAM suite: no
-- depreciation calculation schedule (current_value/depreciation_rate are
-- data fields only, no automatic recalculation), no insurance tracking, no
-- IoT sensor integration (Edge Agent, future), no QR/barcode tagging.
--
-- Tenant model: every table is firm-scoped, mandatory RLS (Rule 3), same
-- convention as every other migration in this repo.

CREATE TABLE assets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    name text NOT NULL,
    asset_number text NOT NULL,
    type text NOT NULL CHECK (type IN ('equipment', 'vehicle', 'property', 'tool', 'other')),
    status text NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'maintenance', 'retired', 'disposed')),
    location_id uuid REFERENCES warehouse_locations (id),
    assigned_to_person_id uuid REFERENCES people (id),
    purchase_date date,
    purchase_price numeric(19,4),
    current_value numeric(19,4),
    depreciation_rate numeric(5,2),
    serial_number text,
    warranty_expires date,
    notes text,
    custom_fields jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, asset_number)
);

CREATE TABLE maintenance_schedules (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    asset_id uuid NOT NULL REFERENCES assets (id) ON DELETE CASCADE,
    name text NOT NULL,
    interval_days integer NOT NULL CHECK (interval_days > 0),
    last_done_at date,
    -- STORED, not computed on read: next_due_at is queried directly by the
    -- daily maintenance-due scheduler (internal/asset's own poll loop,
    -- mirroring internal/workflow/scheduler.go's scheduled_rule_runs
    -- pattern) with a plain `WHERE next_due_at <= $1` - a virtual/computed
    -- column would need the same expression repeated (and indexed) in
    -- every caller instead of one column with a plain btree index.
    next_due_at date GENERATED ALWAYS AS
        (last_done_at + interval_days * INTERVAL '1 day') STORED,
    assigned_to_person_id uuid REFERENCES people (id),
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE maintenance_records (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    asset_id uuid NOT NULL REFERENCES assets (id) ON DELETE CASCADE,
    schedule_id uuid REFERENCES maintenance_schedules (id) ON DELETE SET NULL,
    performed_at date NOT NULL,
    performed_by_person_id uuid REFERENCES people (id),
    description text NOT NULL,
    cost numeric(19,4),
    next_service_date date,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX assets_firm_id_idx ON assets (firm_id);
CREATE INDEX assets_status_idx ON assets (firm_id, status);
CREATE INDEX assets_location_id_idx ON assets (location_id);
CREATE INDEX maintenance_schedules_firm_id_idx ON maintenance_schedules (firm_id);
CREATE INDEX maintenance_schedules_asset_id_idx ON maintenance_schedules (asset_id);
-- The scheduler's own due-check query filters is_active first (a partial
-- index, not a plain btree on next_due_at alone) since inactive schedules
-- should never surface as due regardless of their stale next_due_at.
CREATE INDEX maintenance_schedules_due_idx ON maintenance_schedules (next_due_at) WHERE is_active;
CREATE INDEX maintenance_records_firm_id_idx ON maintenance_records (firm_id);
CREATE INDEX maintenance_records_asset_id_idx ON maintenance_records (asset_id);
CREATE INDEX maintenance_records_schedule_id_idx ON maintenance_records (schedule_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON assets, maintenance_schedules, maintenance_records TO zonaryos_app;

ALTER TABLE assets ENABLE ROW LEVEL SECURITY;
ALTER TABLE maintenance_schedules ENABLE ROW LEVEL SECURITY;
ALTER TABLE maintenance_records ENABLE ROW LEVEL SECURITY;

CREATE POLICY assets_tenant_isolation ON assets
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY maintenance_schedules_tenant_isolation ON maintenance_schedules
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY maintenance_records_tenant_isolation ON maintenance_records
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
