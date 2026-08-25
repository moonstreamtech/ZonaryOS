package models

import (
    "time"
)

type DataRetentionPolicy struct {
    ID            int       `json:"id"`
    FirmID        int       `json:"firm_id"`
    PolicyName    string    `json:"policy_name"`
    DataType      string    `json:"data_type"`
    RetentionDays int       `json:"retention_days"`
    IsActive      bool      `json:"is_active"`
    CreatedAt     time.Time `json:"created_at"`
    UpdatedAt     time.Time `json:"updated_at"`
}

type AuditLogArchive struct {
    ID             int       `json:"id"`
    FirmID         int       `json:"firm_id"`
    OriginalAuditID int      `json:"original_audit_id"`
    Action         string    `json:"action"`
    ResourceType   string    `json:"resource_type"`
    ResourceID     *int      `json:"resource_id"`
    UserID         int       `json:"user_id"`
    IPAddress      *string   `json:"ip_address"`
    UserAgent      *string   `json:"user_agent"`
    Metadata       *string   `json:"metadata"`
    ArchivedAt     time.Time `json:"archived_at"`
}

type ErasureRequest struct {
    ID           int       `json:"id"`
    FirmID       int       `json:"firm_id"`
    SubjectType  string    `json:"subject_type"`
    SubjectID    int       `json:"subject_id"`
    Status       string    `json:"status"`
    RequestedBy  int       `json:"requested_by"`
    Reason       *string   `json:"reason"`
    ProcessedAt  *time.Time `json:"processed_at"`
    CreatedAt    time.Time `json:"created_at"`
}

type DataProcessingRecord struct {
    ID                int       `json:"id"`
    FirmID            int       `json:"firm_id"`
    DataSubjectID     int       `json:"data_subject_id"`
    DataSubjectType   string    `json:"data_subject_type"`
    ProcessingPurpose string    `json:"processing_purpose"`
    LegalBasis        string    `json:"legal_basis"`
    DataCategories    string    `json:"data_categories"`
    Recipients        *string   `json:"recipients"`
    RetentionPeriod   *string   `json:"retention_period"`
    CreatedAt         time.Time `json:"created_at"`
}
