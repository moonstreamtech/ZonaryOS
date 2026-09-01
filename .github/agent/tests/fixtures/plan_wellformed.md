# Add expiry-date tracking to inventory items

## Packages
- internal/inventory

## Migration
migrations/0047_item_expiry.up.sql / .down.sql

## CI Checks
- go build ./...
- go vet ./...
- go test ./...
- python3 scripts/license_headers.py --check

## Steps
- [ ] Add `expiry_date` (nullable date) to the `inventory_items` table via migrations/0047_item_expiry.up.sql and the matching .down.sql. The table already carries an RLS policy scoped to firm_id (see migrations/0012_inventory_items.up.sql) - no new policy needed, this is an added column only.
- [ ] Add `ExpiryDate *time.Time` to the `Item` struct in internal/inventory/items.go and update `scanItem`/the INSERT and UPDATE column lists to include it. Follow the existing nullable-column pattern already used for `Notes` in that same file.
- [ ] Add `ExpiryDate` to the JSON response shape in internal/inventory/handlers.go's item-serialization helper, following the existing field naming convention (camelCase) already used for the other fields there.
