package api

import (
    "encoding/json"
    "net/http"
    "strconv"

    "github.com/gorilla/mux"
    "zonaryos/internal/middleware"
    "zonaryos/internal/models"
    "zonaryos/internal/permission"
    "zonaryos/internal/zdb"
)

type ErasureRequestsHandler struct {
    db *zdb.DB
}

func NewErasureRequestsHandler(db *zdb.DB) *ErasureRequestsHandler {
    return &ErasureRequestsHandler{db: db}
}

func (h *ErasureRequestsHandler) RegisterRoutes(r *mux.Router) {
    r.HandleFunc("/firms/{firmID}/erasure-requests", middleware.WithFirmContext(h.GetErasureRequests)).Methods("GET")
    r.HandleFunc("/firms/{firmID}/erasure-requests", middleware.WithFirmContext(h.CreateErasureRequest)).Methods("POST")
}

func (h *ErasureRequestsHandler) GetErasureRequests(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    firmID, _ := ctx.Value(middleware.FirmContextKey).(int)

    if !permission.IsMember(ctx, firmID) {
        http.Error(w, "Forbidden", http.StatusForbidden)
        return
    }

    rows, err := h.db.Query(ctx, `SELECT * FROM erasure_requests WHERE firm_id = $1`, firmID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    defer rows.Close()

    var requests []models.ErasureRequest
    for rows.Next() {
        var req models.ErasureRequest
        if err := rows.Scan(&req.ID, &req.FirmID, &req.SubjectType, &req.SubjectID, &req.Status, &req.RequestedBy, &req.Reason, &req.ProcessedAt, &req.CreatedAt); err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        requests = append(requests, req)
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(requests)
}

func (h *ErasureRequestsHandler) CreateErasureRequest(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    firmID, _ := ctx.Value(middleware.FirmContextKey).(int)

    if !permission.Has(ctx, firmID, "owner") {
        http.Error(w, "Forbidden", http.StatusForbidden)
        return
    }

    var req models.ErasureRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    req.FirmID = firmID

    err := h.db.QueryRow(ctx,
        `INSERT INTO erasure_requests (firm_id, subject_type, subject_id, requested_by, reason) VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at`,
        req.FirmID, req.SubjectType, req.SubjectID, req.RequestedBy, req.Reason,
    ).Scan(&req.ID, &req.CreatedAt)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusCreated)
    json.NewEncoder(w).Encode(req)
}
