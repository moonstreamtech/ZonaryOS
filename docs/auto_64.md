# Auto for Issue #64

Add employee time tracking with clock-in/out, firm-scoped RLS, and permission-gated access

1. Create migration 0046_time_entries.up.sql with firm-scoped time_entries table (id, firm_id, user_id, clocked_in_at, clocked_out_at, notes, created_at) and RLS policies
2. Create internal/timetracking package with TimeEntry model and CRUD operations using zdb
3. Implement handlers: POST (clock-in with open-entry check), PATCH (clock-out/notes, own entry only), GET list (member sees own, manager sees all via manage_time_entries), GET single
4. Wire routes in router with identity.Middleware and permission checks
5. Add frontend pages for time entry management