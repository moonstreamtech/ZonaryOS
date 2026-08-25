CREATE TABLE firm_notification_preferences (
    firm_id bigint NOT NULL REFERENCES firm(id) ON DELETE CASCADE,
    notification_type text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (firm_id, notification_type)
);

-- Enable RLS
ALTER TABLE firm_notification_preferences ENABLE ROW LEVEL SECURITY;

-- Policy: firm members can manage their own preferences
CREATE POLICY firm_notification_preferences_policy
ON firm_notification_preferences
FOR ALL
USING (firm_id = zdb.get_firm_id())
WITH CHECK (firm_id = zdb.get_firm_id());
