-- Add timezone column to firms table to store the firm's local timezone
-- (e.g., 'UTC', 'America/New_York') for time-sensitive operations and reporting.
ALTER TABLE firms ADD COLUMN timezone text NOT NULL DEFAULT 'UTC';
