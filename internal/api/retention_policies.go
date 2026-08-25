package api

import (
    "encoding/json"
    "net/http"
    "strconv"

    "github.com/gorilla/mux"
    "zonaryos/internal/auth"
    "zonaryos/internal/middleware"
    "zonaryos/internal/models"
    "zonaryos/internal/permission"
    "zonaryos/internal/zdb"
)

type RetentionPoliciesHandler struct {
    db *zdb.DB
}

func NewRetentionPoliciesHandler(db *zdb.DB) *RetentionPoliciesHandler {
    return &RetentionPoliciesHandler{db: db}
}

func (h *RetentionPoliciesHandler) RegisterRoutes(r *mux.Router) {
    r.HandleFunc("/retention-policies", middleware.WithFirmContext(h.GetRetentionPolicies)).Methods("GET")
    r.HandleFunc("/retention-policies", middleware.WithFirmContext(h.CreateRetentionPolicy)).Methods("POST")
    r.HandleFunc("/retention-policies/{id}", middleware.WithFirmContext(h.UpdateRetentionPolicy)).Methods("PUT")
    r.HandleFunc("/retention-policies/{id}", middleware.WithFirmContext(h.DeleteRetentionPolicy)).Methods("DELETE")
}

func (h *RetentionPoliciesHandler) GetRetentionPolicies(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    firmID, _ := ctx.Value(middleware.FirmContextKey).(int)

    if !permission.Has(ctx, firmID, "platform-admin") {
        http.Error(w, "Forbidden", http.StatusForbidden)
        return
    }

    rows, err := h.db.Query(ctx, `SELECT * FROM data_retention_policies WHERE firm_id = $1`, firmID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    defer rows.Close()

    var policies []models.DataRetentionPolicy
    for rows.Next() {
        var p models.DataRetentionPolicy
        if err := rows.Scan(&p.ID, &p.FirmID, &p.PolicyName, &p.DataType, &p.RetentionDays, &p.IsActive, &p.CreatedAt, &p.UpdatedAt); err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        policies = append(policies, p)
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(policies)
}

func (h *RetentionPoliciesHandler) CreateRetentionPolicy(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    firmID, _ := ctx.Value(middleware.FirmContextKey).(int)

    if !permission.Has(ctx, firmID, "platform-admin") {
        http.Error(w, "Forbidden", http.StatusForbidden)
        return
    }

    var p models.DataRetentionPolicy
    if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    p.FirmID = firmID

    err := h.db.QueryRow(ctx,
        `INSERT INTO data_retention_policies (firm_id, policy_name, data_type, retention_days, is_active) VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at, updated_at`,
        p.FirmID, p.PolicyName, p.DataType, p.RetentionDays, p.IsActive,
    ).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusCreated)
    json.NewEncoder(w).Encode(p)
}

func (h *RetentionPoliciesHandler) UpdateRetentionPolicy(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    firmID, _ := ctx.Value(middleware.FirmContextKey).(int)

    if !permission.Has(ctx, firmID, "platform-admin") {
        http.Error(w, "Forbidden", http.StatusForbidden)
        return
    }

    vars := mux.Vars(r)
    id, _ := strconv.Atoi(vars["id"])

    var p models.DataRetentionPolicy
    if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    p.ID = id
    p.FirmID = firmID

    err := h.db.QueryRow(ctx,
        `UPDATE data_retention_policies SET policy_name = $1, data_type = $2, retention_days = $3, is_active = $4, updated_at = NOW() WHERE id = $5 AND firm_id = $6 RETURNING updated_at`,
        p.PolicyName, p.DataType, p.RetentionDays, p.IsActive, p.ID, p.FirmID,
    ).Scan(&p.UpdatedAt)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(p)
}

func (h *RetentionPoliciesHandler) DeleteRetentionPolicy(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    firmID, _ := ctx.Value(middleware.FirmContextKey).(int)

    if !permission.Has(ctx, firmID, "platform-admin") {
        http.Error(w, "Forbidden", http.StatusForbidden)
        return
    }

    vars := mux.Vars(r)
    id, _ := strconv.Atoi(vars["id"])

    _, err := h.db.Exec(ctx, `DELETE FROM data_retention_policies WHERE id = $1 AND firm_id = $2`, id, firmID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.WriteHeader(http.StatusNoContent)
}
