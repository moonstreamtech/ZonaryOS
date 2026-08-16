DROP POLICY cycle_count_lines_tenant_isolation ON cycle_count_lines;
DROP POLICY cycle_count_sessions_tenant_isolation ON cycle_count_sessions;
DROP POLICY inventory_transfers_tenant_isolation ON inventory_transfers;
DROP POLICY warehouse_locations_tenant_isolation ON warehouse_locations;
DROP POLICY warehouses_tenant_isolation ON warehouses;

DROP TABLE cycle_count_lines;
DROP TABLE cycle_count_sessions;
DROP TABLE inventory_transfers;

ALTER TABLE stock_movements ADD COLUMN location text;
ALTER TABLE stock_levels ADD COLUMN location text;

UPDATE stock_movements sm SET location = wl.code FROM warehouse_locations wl WHERE wl.id = sm.location_id;
UPDATE stock_levels sl SET location = wl.code FROM warehouse_locations wl WHERE wl.id = sl.location_id;

ALTER TABLE stock_movements ALTER COLUMN location SET NOT NULL;
ALTER TABLE stock_levels ALTER COLUMN location SET NOT NULL;
ALTER TABLE stock_levels ALTER COLUMN location SET DEFAULT 'default';

ALTER TABLE stock_levels DROP CONSTRAINT stock_levels_firm_id_product_id_location_id_key;
ALTER TABLE stock_levels ADD CONSTRAINT stock_levels_firm_id_product_id_location_key UNIQUE (firm_id, product_id, location);

DROP INDEX stock_movements_location_id_idx;
DROP INDEX stock_levels_location_id_idx;

ALTER TABLE stock_movements DROP COLUMN location_id;
ALTER TABLE stock_levels DROP COLUMN location_id;

DROP TABLE warehouse_locations;
DROP TABLE warehouses;
