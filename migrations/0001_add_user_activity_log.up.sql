-- Add user_activity_log table
CREATE TABLE user_activity_log (
    id BIGSERIAL PRIMARY KEY,
    firm_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    event_type TEXT NOT NULL,
    metadata JSONB,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Enable Row Level Security
ALTER TABLE user_activity_log ENABLE ROW LEVEL SECURITY;

-- Create indexes for performance
CREATE INDEX idx_user_activity_log_firm_id ON user_activity_log(firm_id);
CREATE INDEX idx_user_activity_log_user_id ON user_activity_log(user_id);
CREATE INDEX idx_user_activity_log_event_type ON user_activity_log(event_type);
CREATE INDEX idx_user_activity_log_timestamp ON user_activity_log(timestamp);

-- RLS Policy: firm members can view their firm's activity logs
CREATE POLICY firm_members_can_view_activity ON user_activity_log
    FOR SELECT
    USING (firm_id = current_setting('app.current_firm_id')::BIGINT);
