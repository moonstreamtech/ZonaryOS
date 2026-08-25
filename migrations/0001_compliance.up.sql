-- Data retention policies table
CREATE TABLE data_retention_policies (
    id SERIAL PRIMARY KEY,
    firm_id INTEGER NOT NULL,
    policy_name VARCHAR(255) NOT NULL,
    data_type VARCHAR(100) NOT NULL,
    retention_days INTEGER NOT NULL,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Audit log archive table
CREATE TABLE audit_log_archive (
    id SERIAL PRIMARY KEY,
    firm_id INTEGER NOT NULL,
    original_audit_id INTEGER NOT NULL,
    action VARCHAR(255) NOT NULL,
    resource_type VARCHAR(100) NOT NULL,
    resource_id INTEGER,
    user_id INTEGER NOT NULL,
    ip_address INET,
    user_agent TEXT,
    metadata JSONB,
    archived_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Erasure requests table
CREATE TABLE erasure_requests (
    id SERIAL PRIMARY KEY,
    firm_id INTEGER NOT NULL,
    subject_type VARCHAR(50) NOT NULL, -- 'user', 'customer', etc.
    subject_id INTEGER NOT NULL,
    status VARCHAR(50) DEFAULT 'pending', -- pending, processing, completed, failed
    requested_by INTEGER NOT NULL,
    reason TEXT,
    processed_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Data processing records table
CREATE TABLE data_processing_records (
    id SERIAL PRIMARY KEY,
    firm_id INTEGER NOT NULL,
    data_subject_id INTEGER NOT NULL,
    data_subject_type VARCHAR(50) NOT NULL,
    processing_purpose VARCHAR(255) NOT NULL,
    legal_basis VARCHAR(100) NOT NULL, -- consent, contract, legitimate_interest, etc.
    data_categories JSONB NOT NULL,
    recipients JSONB,
    retention_period VARCHAR(100),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Indexes
CREATE INDEX idx_data_retention_policies_firm_id ON data_retention_policies(firm_id);
CREATE INDEX idx_audit_log_archive_firm_id ON audit_log_archive(firm_id);
CREATE INDEX idx_erasure_requests_firm_id ON erasure_requests(firm_id);
CREATE INDEX idx_data_processing_records_firm_id ON data_processing_records(firm_id);

-- Seed default retention policies
INSERT INTO data_retention_policies (firm_id, policy_name, data_type, retention_days) VALUES
(1, 'Default User Data', 'user_profiles', 365),
(1, 'Default Audit Logs', 'audit_logs', 2555), -- 7 years
(1, 'Default Financial Records', 'financial_transactions', 2555),
(1, 'Default Communication Logs', 'communications', 365);

-- Seed default data processing records
INSERT INTO data_processing_records (firm_id, data_subject_id, data_subject_type, processing_purpose, legal_basis, data_categories) VALUES
(1, 1, 'user', 'Account Management', 'contract', '{"personal_info": true, "contact_info": true}'),
(1, 1, 'user', 'Service Delivery', 'contract', '{"usage_data": true, "preferences": true}');
