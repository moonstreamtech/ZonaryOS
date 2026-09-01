# Add expiry-date tracking to inventory items

## Packages
- internal/inventory

## Migration
migrations/0047_item_expiry.up.sql / .down.sql

## CI Checks
- go build ./...

## Steps
- [x] Add `expiry_date` (nullable date) to the `inventory_items` table via migrations/0047_item_expiry.up.sql and the matching .down.sql. The table already carries an RLS policy scoped to firm_id - no new policy needed, this is an added column only.
- [ ] Add `ExpiryDate *time.Time` to the `Item` struct in internal/inventory/items.go and update scanItem/the INSERT and UPDATE column lists - reworded by Kaan before merging to also mention the repository test fixtures need updating.
