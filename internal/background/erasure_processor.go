package background

import (
    "context"
    "log"
    "time"

    "zonaryos/internal/zdb"
)

type ErasureProcessor struct {
    db *zdb.DB
}

func NewErasureProcessor(db *zdb.DB) *ErasureProcessor {
    return &ErasureProcessor{db: db}
}

func (e *ErasureProcessor) Start() {
    go func() {
        ticker := time.NewTicker(time.Hour) // Check hourly
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                e.ProcessPendingRequests()
            }
        }
    }()
}

func (e *ErasureProcessor) ProcessPendingRequests() {
    ctx := context.Background()

    rows, err := e.db.Query(ctx, `SELECT id, firm_id, subject_type, subject_id FROM erasure_requests WHERE status = 'pending'`)
    if err != nil {
        log.Printf("Erasure processor error: %v", err)
        return
    }
    defer rows.Close()

    for rows.Next() {
        var id, firmID, subjectID int
        var subjectType string
        if err := rows.Scan(&id, &firmID, &subjectType, &subjectID); err != nil {
            log.Printf("Erasure processor scan error: %v", err)
            continue
        }

        e.processErasureRequest(id, firmID, subjectType, subjectID)
    }
}

func (e *ErasureProcessor) processErasureRequest(id, firmID int, subjectType, subjectID string) {
    ctx := context.Background()

    // Update status to processing
    _, err := e.db.Exec(ctx, `UPDATE erasure_requests SET status = 'processing' WHERE id = $1`, id)
    if err != nil {
        log.Printf("Update erasure request status error: %v", err)
        return
    }

    // Perform erasure based on subject type
    switch subjectType {
    case "user":
        e.eraseUserData(firmID, subjectID)
    case "customer":
        e.eraseCustomerData(firmID, subjectID)
    default:
        log.Printf("Unknown subject type: %s", subjectType)
    }

    // Mark as completed
    _, err = e.db.Exec(ctx, `UPDATE erasure_requests SET status = 'completed', processed_at = NOW() WHERE id = $1`, id)
    if err != nil {
        log.Printf("Complete erasure request error: %v", err)
    }
}

func (e *ErasureProcessor) eraseUserData(firmID, userID int) {
    // Anonymize or delete user data
    // Example: Nullify PII in user profiles
    _, err := e.db.Exec(context.Background(),
        `UPDATE user_profiles SET first_name = 'ANONYMIZED', last_name = 'ANONYMIZED', email = 'ANONYMIZED@ANONYMIZED.COM' WHERE firm_id = $1 AND user_id = $2`,
        firmID, userID,
    )
    if err != nil {
        log.Printf("Erase user data error: %v", err)
    }

    // Mark related audit logs
    _, err = e.db.Exec(context.Background(),
        `UPDATE audit_logs SET metadata = jsonb_set(metadata, '{erased}', 'true', true) WHERE firm_id = $1 AND user_id = $2`,
        firmID, userID,
    )
    if err != nil {
        log.Printf("Mark audit logs error: %v", err)
    }
}

func (e *ErasureProcessor) eraseCustomerData(firmID, customerID int) {
    // Implementation for customer data erasure
    log.Printf("Erasing customer data for firm %d, customer %d", firmID, customerID)
}
