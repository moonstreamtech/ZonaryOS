# Auto for Issue #64

Add employee time tracking with clock-in/clock-out, firm-scoped RLS, and manager permissions.

1. Create migration 0046_time_entries.up.sql with time_entries table (id, firm_id, user_id, clocked_in_at, clocked_out_at, notes, created_at), enable RLS, and add firm-scoped policies.
2. Create internal/timetracking/timetracking.go with TimeEntry struct, Store interface, and business logic (clock-in/out, single open entry check, duration calculation).
3. Create internal/timetracking/handlers.go with HTTP handlers for POST/PATCH/GET endpoints, using identity middleware and permission checks.
4. Register routes in internal/timetracking/routes.go (or similar) and wire into the existing router (if required by project structure).
5. Add manage_time_entries permission constant in the permission package if needed.