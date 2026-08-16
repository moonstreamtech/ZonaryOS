-- Warehouse management, location hierarchy, inventory transfers, and
-- cycle counting: real warehouse structure under the existing
-- products/stock_levels layer (migrations/0011_inventory_core.up.sql),
-- which until now only had a free-text stock_levels.location field.
--
-- Tenant model: every table is firm-scoped, mandatory RLS (Rule 3), same
-- convention as every other migration in this repo.
--
-- Scope boundary: no barcode scanning (Edge Agent, future), no
-- FIFO/FEFO lot tracking, no multi-warehouse purchase order routing, no
-- picking list generation.

CREATE TABLE warehouses (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    name text NOT NULL,
    address text,
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- code, e.g. "A-01-03" (aisle-shelf-bin). parent_id builds the
-- collapsible aisle -> shelf -> bin tree the frontend renders. Not
-- enforced by a CHECK that parent.type is one level up from type (an
-- aisle's parent must itself be null/another aisle in this simple model,
-- a shelf's parent an aisle, a bin's parent a shelf/aisle) - the
-- application (internal/warehouse.CreateLocation) validates that
-- shape, the same "app validates the open vocabulary, not the schema"
-- judgment call stock_movements.reason already makes.
--
-- is_default marks the one location AdjustStockTx (internal/inventory)
-- resolves to when a caller doesn't have a specific location in context
-- (e.g. the stock_to_sale/manufacturing workflow bridges, which have no
-- location concept of their own yet) - exactly one per firm, enforced by
-- the partial unique index below.
CREATE TABLE warehouse_locations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    warehouse_id uuid NOT NULL REFERENCES warehouses (id) ON DELETE CASCADE,
    code text NOT NULL,
    name text,
    type text NOT NULL DEFAULT 'bin'
        CHECK (type IN ('aisle', 'shelf', 'bin', 'staging', 'dispatch')),
    parent_id uuid REFERENCES warehouse_locations (id) ON DELETE SET NULL,
    is_default boolean NOT NULL DEFAULT false,
    is_active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (firm_id, warehouse_id, code)
);

CREATE UNIQUE INDEX warehouse_locations_one_default_per_firm
    ON warehouse_locations (firm_id) WHERE is_default;

-- ---------------------------------------------------------------------
-- stock_levels.location (free text) -> stock_levels.location_id (FK):
-- a breaking schema change, handled the safe way - add the new column
-- nullable, backfill every existing row from a real warehouse_locations
-- row, THEN make it NOT NULL and drop the old column. No window where a
-- concurrent write could see a half-migrated row: this is a single
-- migration transaction (internal/platform/db.Migrate runs each
-- migration file in one transaction via golang-migrate), so every step
-- below is atomic together - either the whole rename completes or none
-- of it does.
-- ---------------------------------------------------------------------

ALTER TABLE stock_levels ADD COLUMN location_id uuid REFERENCES warehouse_locations (id);
ALTER TABLE stock_movements ADD COLUMN location_id uuid REFERENCES warehouse_locations (id);

-- One default warehouse + one default ("default"-coded) location per
-- EXISTING firm - not just firms that already have stock_levels rows.
-- AdjustStockTx now always needs a resolvable default location for any
-- firm it's called against (the stock_to_sale/manufacturing bridges have
-- no location of their own to pass), so every firm gets one
-- unconditionally, the same "seeded for every firm regardless of wizard
-- answers" tier internal/accounting.coreAccounts already uses - a firm
-- that never explicitly set up warehouses still has stock somewhere.
INSERT INTO warehouses (firm_id, name)
SELECT id, 'Default Warehouse' FROM firms;

INSERT INTO warehouse_locations (firm_id, warehouse_id, code, name, type, is_default)
SELECT w.firm_id, w.id, 'default', 'Main', 'bin', true
FROM warehouses w
WHERE w.name = 'Default Warehouse';

-- Any OTHER distinct location text already in use (e.g. a firm that had
-- called the stock API with a custom location string like
-- "warehouse-2") gets its own non-default location under that same
-- default warehouse, keyed by the literal text as its code - so the
-- backfill UPDATEs below can join on (firm_id, code) uniformly for
-- every existing row, "default" included.
INSERT INTO warehouse_locations (firm_id, warehouse_id, code, name, type, is_default)
SELECT DISTINCT sl.firm_id, w.id, sl.location, sl.location, 'bin', false
FROM stock_levels sl
JOIN warehouses w ON w.firm_id = sl.firm_id AND w.name = 'Default Warehouse'
WHERE sl.location <> 'default'
ON CONFLICT (firm_id, warehouse_id, code) DO NOTHING;

INSERT INTO warehouse_locations (firm_id, warehouse_id, code, name, type, is_default)
SELECT DISTINCT sm.firm_id, w.id, sm.location, sm.location, 'bin', false
FROM stock_movements sm
JOIN warehouses w ON w.firm_id = sm.firm_id AND w.name = 'Default Warehouse'
WHERE sm.location <> 'default'
ON CONFLICT (firm_id, warehouse_id, code) DO NOTHING;

UPDATE stock_levels sl
SET location_id = wl.id
FROM warehouse_locations wl
WHERE wl.firm_id = sl.firm_id AND wl.code = sl.location;

UPDATE stock_movements sm
SET location_id = wl.id
FROM warehouse_locations wl
WHERE wl.firm_id = sm.firm_id AND wl.code = sm.location;

ALTER TABLE stock_levels ALTER COLUMN location_id SET NOT NULL;
ALTER TABLE stock_movements ALTER COLUMN location_id SET NOT NULL;

ALTER TABLE stock_levels DROP CONSTRAINT stock_levels_firm_id_product_id_location_key;
ALTER TABLE stock_levels ADD CONSTRAINT stock_levels_firm_id_product_id_location_id_key UNIQUE (firm_id, product_id, location_id);

-- migration-safety:acknowledge: same reasoning migrations/0017_workflow_effects_refactor.up.sql's
-- own acknowledgment gives - this system has no decided deployment
-- target/rolling-deploy infrastructure yet (docs/OPEN_POINTS.md item 34,
-- Canary/Rollback Trigger explicitly "Not Set Up"), so there is no live
-- primary/replica window today where an old binary could still be
-- reading the text `location` column after this migration runs. Every
-- application code path that read/wrote it (internal/inventory.AdjustStockTx
-- and its three callers, internal/portability's export) is updated to
-- location_id in this SAME batch/commit, not a later one - so there is
-- no in-between state where deployed code expects the dropped column to
-- still exist.
ALTER TABLE stock_levels DROP COLUMN location;
ALTER TABLE stock_movements DROP COLUMN location;

CREATE INDEX stock_levels_location_id_idx ON stock_levels (location_id);
CREATE INDEX stock_movements_location_id_idx ON stock_movements (location_id);

-- ---------------------------------------------------------------------
-- Inventory transfers
-- ---------------------------------------------------------------------

CREATE TABLE inventory_transfers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    from_location_id uuid NOT NULL REFERENCES warehouse_locations (id),
    to_location_id uuid NOT NULL REFERENCES warehouse_locations (id),
    product_id uuid NOT NULL REFERENCES products (id),
    quantity numeric(10, 4) NOT NULL CHECK (quantity > 0),
    status text NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'in_transit', 'completed', 'cancelled')),
    notes text,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------
-- Cycle counting
-- ---------------------------------------------------------------------

CREATE TABLE cycle_count_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    warehouse_id uuid NOT NULL REFERENCES warehouses (id),
    status text NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'completed')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE cycle_count_lines (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    firm_id uuid NOT NULL REFERENCES firms (id) ON DELETE CASCADE,
    session_id uuid NOT NULL REFERENCES cycle_count_sessions (id) ON DELETE CASCADE,
    location_id uuid NOT NULL REFERENCES warehouse_locations (id),
    product_id uuid NOT NULL REFERENCES products (id),
    system_quantity numeric(10, 4) NOT NULL,
    counted_quantity numeric(10, 4),
    variance numeric(10, 4) GENERATED ALWAYS AS (counted_quantity - system_quantity) STORED,
    adjusted boolean NOT NULL DEFAULT false
);

CREATE INDEX warehouses_firm_id_idx ON warehouses (firm_id);
CREATE INDEX warehouse_locations_firm_id_idx ON warehouse_locations (firm_id);
CREATE INDEX warehouse_locations_warehouse_id_idx ON warehouse_locations (firm_id, warehouse_id);
CREATE INDEX warehouse_locations_parent_id_idx ON warehouse_locations (parent_id);
CREATE INDEX inventory_transfers_firm_id_idx ON inventory_transfers (firm_id);
CREATE INDEX inventory_transfers_status_idx ON inventory_transfers (firm_id, status);
CREATE INDEX cycle_count_sessions_firm_id_idx ON cycle_count_sessions (firm_id);
CREATE INDEX cycle_count_lines_session_id_idx ON cycle_count_lines (session_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON warehouses, warehouse_locations TO zonaryos_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON inventory_transfers TO zonaryos_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON cycle_count_sessions, cycle_count_lines TO zonaryos_app;

ALTER TABLE warehouses ENABLE ROW LEVEL SECURITY;
ALTER TABLE warehouse_locations ENABLE ROW LEVEL SECURITY;
ALTER TABLE inventory_transfers ENABLE ROW LEVEL SECURITY;
ALTER TABLE cycle_count_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE cycle_count_lines ENABLE ROW LEVEL SECURITY;

CREATE POLICY warehouses_tenant_isolation ON warehouses
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY warehouse_locations_tenant_isolation ON warehouse_locations
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY inventory_transfers_tenant_isolation ON inventory_transfers
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY cycle_count_sessions_tenant_isolation ON cycle_count_sessions
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());

CREATE POLICY cycle_count_lines_tenant_isolation ON cycle_count_lines
    USING (firm_id = app_current_firm_id())
    WITH CHECK (firm_id = app_current_firm_id());
