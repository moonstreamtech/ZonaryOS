package api

import (
    "encoding/json"
    "net/http"

    "github.com/gorilla/mux"
    "zonaryos/internal/middleware"
    "zonaryos/internal/models"
    "zonaryos/internal/permission"
    "zonaryos/internal/zdb"
)

type DataProcessingRecordsHandler struct {
    db *zdb.DB
}

func NewDataProcessingRecordsHandler(db *zdb.DB) *DataProcessingRecordsHandler {
    return &DataProcessingRecordsHandler{db: db}
}

func (h *DataProcessingRecordsHandler) RegisterRoutes(r *mux.Router) {
    r.HandleFunc("/firms/{firmID}/data-processing-records", middleware.WithFirmContext(h.GetDataProcessingRecords)).Methods("GET")
}

func (h *DataProcessingRecordsHandler) GetDataProcessingRecords(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    firmID, _ := ctx.Value(middleware.FirmContextKey).(int)

    if !permission.IsMember(ctx, firmID) {
        http.Error(w, "Forbidden", http.StatusForbidden)
        return
    }

    rows, err := h.db.Query(ctx, `SELECT * FROM data_processing_records WHERE firm_id = $1`, firmID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    defer rows.Close()

    var records []models.DataProcessingRecord
    for rows.Next() {
        var rec models.DataProcessingRecord
        if err := rows.Scan(&rec.ID, &rec.FirmID, &rec.DataSubjectID, &rec.DataSubjectType, &rec.ProcessingPurpose, &rec.LegalBasis, &rec.DataCategories, &rec.Recipients, &rec.RetentionPeriod, &rec.CreatedAt); err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        records = append(records, rec)
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(records)
}
