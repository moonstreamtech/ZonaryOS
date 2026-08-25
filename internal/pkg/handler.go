package pkg

import (
    "encoding/json"
    "net/http"

    "github.com/jackc/pgx/v5"
    "github.com/zonaryco/zdb"
    "github.com/zonaryco/permission"
)

type NotificationPreference struct {
    FirmID           int64  `json:"firm_id"`
    NotificationType string `json:"notification_type"`
    Enabled          bool   `json:"enabled"`
}

// GetNotificationPreferences handles GET requests for firm notification preferences.
func GetNotificationPreferences(w http.ResponseWriter, r *http.Request) {
    firmID, err := zdb.GetFirmID(r.Context())
    if err != nil {
        http.Error(w, "Unauthorized", http.StatusUnauthorized)
        return
    }

    if !permission.IsMember(r.Context(), firmID) {
        http.Error(w, "Forbidden", http.StatusForbidden)
        return
    }

    pool := zdb.GetPool(r.Context())
    rows, err := pool.Query(r.Context(), `SELECT firm_id, notification_type, enabled FROM firm_notification_preferences WHERE firm_id = $1`, firmID)
    if err != nil {
        http.Error(w, "Internal Server Error", http.StatusInternalServerError)
        return
    }
    defer rows.Close()

    var preferences []NotificationPreference
    for rows.Next() {
        var p NotificationPreference
        if err := rows.Scan(&p.FirmID, &p.NotificationType, &p.Enabled); err != nil {
            http.Error(w, "Internal Server Error", http.StatusInternalServerError)
            return
        }
        preferences = append(preferences, p)
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(preferences)
}

// UpdateNotificationPreferences handles PUT requests for firm notification preferences.
func UpdateNotificationPreferences(w http.ResponseWriter, r *http.Request) {
    firmID, err := zdb.GetFirmID(r.Context())
    if err != nil {
        http.Error(w, "Unauthorized", http.StatusUnauthorized)
        return
    }

    if !permission.Has(r.Context(), firmID, "firm:notification:write") {
        http.Error(w, "Forbidden", http.StatusForbidden)
        return
    }

    var preferences []NotificationPreference
    if err := json.NewDecoder(r.Body).Decode(&preferences); err != nil {
        http.Error(w, "Bad Request", http.StatusBadRequest)
        return
    }

    pool := zdb.GetPool(r.Context())
    tx, err := pool.Begin(r.Context())
    if err != nil {
        http.Error(w, "Internal Server Error", http.StatusInternalServerError)
        return
    }
    defer tx.Rollback(r.Context())

    for _, p := range preferences {
        _, err := tx.Exec(r.Context(), `INSERT INTO firm_notification_preferences (firm_id, notification_type, enabled) VALUES ($1, $2, $3) ON CONFLICT (firm_id, notification_type) DO UPDATE SET enabled = $3, updated_at = now()`, p.FirmID, p.NotificationType, p.Enabled)
        if err != nil {
            http.Error(w, "Internal Server Error", http.StatusInternalServerError)
            return
        }
    }

    if err := tx.Commit(r.Context()); err != nil {
        http.Error(w, "Internal Server Error", http.StatusInternalServerError)
        return
    }

    w.WriteHeader(http.StatusOK)
}
