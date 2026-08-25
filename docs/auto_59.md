# Auto for Issue #59

Add a dashboard widget displaying recent user activity for a firm, including a paginated, filterable log of events.

1. Create the `user_activity_log` table with columns for id, firm_id, user_id, event_type, event_data (JSONB), and created_at. Enable Row Level Security (RLS) and ensure firm_id is NOT NULL. 2. Write the SQL migration file `0001_user_activity_log.up.sql`. 3. Implement the GET `/api/firms/{firmID}/activity` HTTP endpoint in Go, gated by member permissions, supporting query parameters for pagination (limit/offset) and filtering (user_id, event_type). 4. Develop the frontend Activity widget component in Next.js, integrating i18n keys for the widget title and empty state. 5. Add the widget to the main dashboard page.