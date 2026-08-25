package background

import (
    "context"
    "log"
    "time"

    "zonaryos/internal/zdb"
)

type RetentionJob struct {
    db *zdb.DB
}

func NewRetentionJob(db *zdb.DB) *RetentionJob {
    return &RetentionJob{db: db}
}

func (j *RetentionJob) Start() {
    go func() {
        ticker := time.NewTicker(30 * 24 * time.Hour) // Monthly
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                j.Run()
            }
        }
    }()
}

func (j *RetentionJob) Run() {
    ctx := context.Background()

    // Get all active retention policies
    rows, err := j.db.Query(ctx, `SELECT id, firm_id, data_type, retention_days FROM data_retention_policies WHERE is_active = true`)
    if err != nil {
        log.Printf("Retention job error: %v", err)
        return
    }
    defer rows.Close()

    for rows.Next() {
        var policyID, firmID, retentionDays int
        var dataType string
        if err := rows.Scan(&policyID, &firmID, &retentionDays, &dataType); err != nil {
            log.Printf("Retention job scan error: %v", err)
            continue
        }

        // Implement retention logic based on data type
        switch dataType {
        case "audit_logs":
            j.archiveAuditLogs(firmID, retentionDays)
        case "user_profiles":
            j.retainUserData(firmID, retentionDays)
        // Add more data types as needed
        }
    }
}

func (j *RetentionJob) archiveAuditLogs(firmID, retentionDays int) {
    // Implementation to archive old audit logs
    ctx := context.Background()
    _, err := j.db.Exec(ctx,
        `INSERT INTO audit_log_archive (firm_id, original_audit_id, action, resource_type, resource_id, user_id, ip_address, user_agent, metadata)
         SELECT firm_id, id, action, resource_type, resource_id, user_id, ip_address, user_agent, metadata
         FROM audit_logs
         WHERE firm_id = $1 AND created_at < NOW() - INTERVAL '$2 days'`,
        firmID, retentionDays,
    )
    if err != nil {
        log.Printf("Archive audit logs error: %v", err)
    }

    // Delete archived logs
    _, err = j.db.Exec(ctx,
        `DELETE FROM audit_logs WHERE firm_id = $1 AND created_at < NOW() - INTERVAL '$2 days'`,
        firmID, retentionDays,
    )
    if err != nil {
        log.Printf("Delete audit logs error: %v", err)
    }
}

func (j *RetentionJob) retainUserData(firmID, retentionDays int) {
    // Implementation to handle user data retention
    // This might involve anonymizing or deleting old user data
    log.Printf("Retention for user data in firm %d: %d days", firmID, retentionDays)
}
