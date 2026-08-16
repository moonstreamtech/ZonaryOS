// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package asset

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// RegisterRoutes wires internal/asset's HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention as
// every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/assets", auth(http.HandlerFunc(handleListAssets(pool))))
	mux.Handle("POST /api/firms/{firmID}/assets", auth(http.HandlerFunc(handleCreateAsset(pool))))
	mux.Handle("GET /api/firms/{firmID}/assets/{assetID}", auth(http.HandlerFunc(handleGetAsset(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/assets/{assetID}", auth(http.HandlerFunc(handleUpdateAsset(pool))))

	mux.Handle("GET /api/firms/{firmID}/assets/{assetID}/maintenance-schedules", auth(http.HandlerFunc(handleListMaintenanceSchedules(pool))))
	mux.Handle("POST /api/firms/{firmID}/assets/{assetID}/maintenance-schedules", auth(http.HandlerFunc(handleCreateMaintenanceSchedule(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/maintenance-schedules/{scheduleID}", auth(http.HandlerFunc(handleUpdateMaintenanceSchedule(pool))))

	mux.Handle("GET /api/firms/{firmID}/assets/{assetID}/maintenance-records", auth(http.HandlerFunc(handleListMaintenanceRecords(pool))))
	mux.Handle("POST /api/firms/{firmID}/assets/{assetID}/maintenance-records", auth(http.HandlerFunc(handleCreateMaintenanceRecord(pool))))
}

// writeAssetError maps this package's sentinel errors to the HTTP status
// that reflects why the caller isn't getting what they asked for - same
// convention as internal/warehouse's writeWarehouseError.
func writeAssetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrAssetNotFound), errors.Is(err, ErrMaintenanceScheduleNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidAsset), errors.Is(err, ErrDuplicateAssetNumber),
		errors.Is(err, ErrInvalidMaintenanceSchedule), errors.Is(err, ErrInvalidMaintenanceRecord):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func resolveIdentity(r *http.Request, pool *pgxpool.Pool) (firmID, userID uuid.UUID, ok bool, status int, msg string) {
	id, present := identity.FromContext(r.Context())
	if !present {
		return uuid.UUID{}, uuid.UUID{}, false, http.StatusInternalServerError, "missing identity"
	}
	firmID, err := uuid.Parse(r.PathValue("firmID"))
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, false, http.StatusBadRequest, "invalid firm id"
	}
	userID, err = identity.ResolveOrCreateUser(r.Context(), pool, id)
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, false, http.StatusInternalServerError, "failed to resolve user"
	}
	return firmID, userID, true, 0, ""
}

func formatDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format("2006-01-02")
	return &s
}

func uuidString(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}

// ---------------------------------------------------------------------
// Assets
// ---------------------------------------------------------------------

type assetResponse struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	AssetNumber        string         `json:"assetNumber"`
	Type               string         `json:"type"`
	Status             string         `json:"status"`
	LocationID         *string        `json:"locationId,omitempty"`
	AssignedToPersonID *string        `json:"assignedToPersonId,omitempty"`
	PurchaseDate       *string        `json:"purchaseDate,omitempty"`
	PurchasePrice      *string        `json:"purchasePrice,omitempty"`
	CurrentValue       *string        `json:"currentValue,omitempty"`
	DepreciationRate   *string        `json:"depreciationRate,omitempty"`
	SerialNumber       *string        `json:"serialNumber,omitempty"`
	WarrantyExpires    *string        `json:"warrantyExpires,omitempty"`
	Notes              *string        `json:"notes,omitempty"`
	CustomFields       map[string]any `json:"customFields"`
	CreatedAt          string         `json:"createdAt"`
}

func toAssetResponse(a Asset) assetResponse {
	return assetResponse{
		ID: a.ID.String(), Name: a.Name, AssetNumber: a.AssetNumber, Type: a.Type, Status: a.Status,
		LocationID: uuidString(a.LocationID), AssignedToPersonID: uuidString(a.AssignedToPersonID),
		PurchaseDate: formatDate(a.PurchaseDate), PurchasePrice: a.PurchasePrice, CurrentValue: a.CurrentValue,
		DepreciationRate: a.DepreciationRate, SerialNumber: a.SerialNumber, WarrantyExpires: formatDate(a.WarrantyExpires),
		Notes: a.Notes, CustomFields: a.CustomFields, CreatedAt: a.CreatedAt.Format(time.RFC3339),
	}
}

func handleListAssets(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		assets, err := ListAssets(r.Context(), pool, firmID, userID)
		if err != nil {
			writeAssetError(w, err)
			return
		}
		resp := make([]assetResponse, 0, len(assets))
		for _, a := range assets {
			resp = append(resp, toAssetResponse(a))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type assetRequest struct {
	Name               string         `json:"name"`
	AssetNumber        string         `json:"assetNumber"`
	Type               string         `json:"type"`
	Status             string         `json:"status"`
	LocationID         *uuid.UUID     `json:"locationId"`
	AssignedToPersonID *uuid.UUID     `json:"assignedToPersonId"`
	PurchaseDate       string         `json:"purchaseDate"`
	PurchasePrice      *string        `json:"purchasePrice"`
	CurrentValue       *string        `json:"currentValue"`
	DepreciationRate   *string        `json:"depreciationRate"`
	SerialNumber       string         `json:"serialNumber"`
	WarrantyExpires    string         `json:"warrantyExpires"`
	Notes              string         `json:"notes"`
	CustomFields       map[string]any `json:"customFields"`
}

func handleCreateAsset(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req assetRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		a, err := CreateAsset(r.Context(), pool, firmID, userID, CreateAssetInput{
			Name: req.Name, AssetNumber: req.AssetNumber, Type: req.Type, Status: req.Status,
			LocationID: req.LocationID, AssignedToPersonID: req.AssignedToPersonID, PurchaseDate: req.PurchaseDate,
			PurchasePrice: req.PurchasePrice, CurrentValue: req.CurrentValue, DepreciationRate: req.DepreciationRate,
			SerialNumber: req.SerialNumber, WarrantyExpires: req.WarrantyExpires, Notes: req.Notes, CustomFields: req.CustomFields,
		})
		if err != nil {
			writeAssetError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toAssetResponse(a))
	}
}

func handleGetAsset(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		assetID, err := uuid.Parse(r.PathValue("assetID"))
		if err != nil {
			http.Error(w, "invalid asset id", http.StatusBadRequest)
			return
		}
		a, err := GetAsset(r.Context(), pool, firmID, userID, assetID)
		if err != nil {
			writeAssetError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toAssetResponse(a))
	}
}

type updateAssetRequest struct {
	Name                  *string        `json:"name"`
	Status                *string        `json:"status"`
	LocationID            *uuid.UUID     `json:"locationId"`
	HasLocationID         bool           `json:"hasLocationId"`
	AssignedToPersonID    *uuid.UUID     `json:"assignedToPersonId"`
	HasAssignedToPersonID bool           `json:"hasAssignedToPersonId"`
	PurchaseDate          *string        `json:"purchaseDate"`
	PurchasePrice         *string        `json:"purchasePrice"`
	CurrentValue          *string        `json:"currentValue"`
	DepreciationRate      *string        `json:"depreciationRate"`
	SerialNumber          *string        `json:"serialNumber"`
	WarrantyExpires       *string        `json:"warrantyExpires"`
	Notes                 *string        `json:"notes"`
	CustomFields          map[string]any `json:"customFields"`
}

func handleUpdateAsset(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		assetID, err := uuid.Parse(r.PathValue("assetID"))
		if err != nil {
			http.Error(w, "invalid asset id", http.StatusBadRequest)
			return
		}
		var req updateAssetRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		a, err := UpdateAsset(r.Context(), pool, firmID, userID, assetID, UpdateAssetInput{
			Name: req.Name, Status: req.Status, LocationID: req.LocationID, HasLocationID: req.HasLocationID,
			AssignedToPersonID: req.AssignedToPersonID, HasAssignedTo: req.HasAssignedToPersonID,
			PurchaseDate: req.PurchaseDate, PurchasePrice: req.PurchasePrice, CurrentValue: req.CurrentValue,
			DepreciationRate: req.DepreciationRate, SerialNumber: req.SerialNumber, WarrantyExpires: req.WarrantyExpires,
			Notes: req.Notes, CustomFields: req.CustomFields,
		})
		if err != nil {
			writeAssetError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toAssetResponse(a))
	}
}

// ---------------------------------------------------------------------
// Maintenance schedules
// ---------------------------------------------------------------------

type maintenanceScheduleResponse struct {
	ID                 string  `json:"id"`
	AssetID            string  `json:"assetId"`
	Name               string  `json:"name"`
	IntervalDays       int     `json:"intervalDays"`
	LastDoneAt         *string `json:"lastDoneAt,omitempty"`
	NextDueAt          *string `json:"nextDueAt,omitempty"`
	AssignedToPersonID *string `json:"assignedToPersonId,omitempty"`
	IsActive           bool    `json:"isActive"`
	CreatedAt          string  `json:"createdAt"`
}

func toMaintenanceScheduleResponse(s MaintenanceSchedule) maintenanceScheduleResponse {
	return maintenanceScheduleResponse{
		ID: s.ID.String(), AssetID: s.AssetID.String(), Name: s.Name, IntervalDays: s.IntervalDays,
		LastDoneAt: formatDate(s.LastDoneAt), NextDueAt: formatDate(s.NextDueAt),
		AssignedToPersonID: uuidString(s.AssignedToPersonID), IsActive: s.IsActive, CreatedAt: s.CreatedAt.Format(time.RFC3339),
	}
}

func handleListMaintenanceSchedules(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		assetID, err := uuid.Parse(r.PathValue("assetID"))
		if err != nil {
			http.Error(w, "invalid asset id", http.StatusBadRequest)
			return
		}
		schedules, err := ListMaintenanceSchedules(r.Context(), pool, firmID, userID, assetID)
		if err != nil {
			writeAssetError(w, err)
			return
		}
		resp := make([]maintenanceScheduleResponse, 0, len(schedules))
		for _, s := range schedules {
			resp = append(resp, toMaintenanceScheduleResponse(s))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type maintenanceScheduleRequest struct {
	Name               string     `json:"name"`
	IntervalDays       int        `json:"intervalDays"`
	LastDoneAt         string     `json:"lastDoneAt"`
	AssignedToPersonID *uuid.UUID `json:"assignedToPersonId"`
}

func handleCreateMaintenanceSchedule(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		assetID, err := uuid.Parse(r.PathValue("assetID"))
		if err != nil {
			http.Error(w, "invalid asset id", http.StatusBadRequest)
			return
		}
		var req maintenanceScheduleRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		s, err := CreateMaintenanceSchedule(r.Context(), pool, firmID, userID, assetID, CreateMaintenanceScheduleInput{
			Name: req.Name, IntervalDays: req.IntervalDays, LastDoneAt: req.LastDoneAt, AssignedToPersonID: req.AssignedToPersonID,
		})
		if err != nil {
			writeAssetError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toMaintenanceScheduleResponse(s))
	}
}

type updateMaintenanceScheduleRequest struct {
	Name                  *string    `json:"name"`
	IntervalDays          *int       `json:"intervalDays"`
	AssignedToPersonID    *uuid.UUID `json:"assignedToPersonId"`
	HasAssignedToPersonID bool       `json:"hasAssignedToPersonId"`
	IsActive              *bool      `json:"isActive"`
}

func handleUpdateMaintenanceSchedule(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		scheduleID, err := uuid.Parse(r.PathValue("scheduleID"))
		if err != nil {
			http.Error(w, "invalid schedule id", http.StatusBadRequest)
			return
		}
		var req updateMaintenanceScheduleRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		s, err := UpdateMaintenanceSchedule(r.Context(), pool, firmID, userID, scheduleID, UpdateMaintenanceScheduleInput{
			Name: req.Name, IntervalDays: req.IntervalDays, AssignedToPersonID: req.AssignedToPersonID,
			HasAssignedTo: req.HasAssignedToPersonID, IsActive: req.IsActive,
		})
		if err != nil {
			writeAssetError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toMaintenanceScheduleResponse(s))
	}
}

// ---------------------------------------------------------------------
// Maintenance records
// ---------------------------------------------------------------------

type maintenanceRecordResponse struct {
	ID                  string  `json:"id"`
	AssetID             string  `json:"assetId"`
	ScheduleID          *string `json:"scheduleId,omitempty"`
	PerformedAt         string  `json:"performedAt"`
	PerformedByPersonID *string `json:"performedByPersonId,omitempty"`
	Description         string  `json:"description"`
	Cost                *string `json:"cost,omitempty"`
	NextServiceDate     *string `json:"nextServiceDate,omitempty"`
	CreatedAt           string  `json:"createdAt"`
}

func toMaintenanceRecordResponse(r MaintenanceRecord) maintenanceRecordResponse {
	performedAt := r.PerformedAt
	return maintenanceRecordResponse{
		ID: r.ID.String(), AssetID: r.AssetID.String(), ScheduleID: uuidString(r.ScheduleID),
		PerformedAt: performedAt.Format("2006-01-02"), PerformedByPersonID: uuidString(r.PerformedByPersonID),
		Description: r.Description, Cost: r.Cost, NextServiceDate: formatDate(r.NextServiceDate),
		CreatedAt: r.CreatedAt.Format(time.RFC3339),
	}
}

func handleListMaintenanceRecords(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		assetID, err := uuid.Parse(r.PathValue("assetID"))
		if err != nil {
			http.Error(w, "invalid asset id", http.StatusBadRequest)
			return
		}
		records, err := ListMaintenanceRecords(r.Context(), pool, firmID, userID, assetID)
		if err != nil {
			writeAssetError(w, err)
			return
		}
		resp := make([]maintenanceRecordResponse, 0, len(records))
		for _, rec := range records {
			resp = append(resp, toMaintenanceRecordResponse(rec))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type maintenanceRecordRequest struct {
	ScheduleID          *uuid.UUID `json:"scheduleId"`
	PerformedAt         string     `json:"performedAt"`
	PerformedByPersonID *uuid.UUID `json:"performedByPersonId"`
	Description         string     `json:"description"`
	Cost                *string    `json:"cost"`
	NextServiceDate     string     `json:"nextServiceDate"`
}

func handleCreateMaintenanceRecord(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		assetID, err := uuid.Parse(r.PathValue("assetID"))
		if err != nil {
			http.Error(w, "invalid asset id", http.StatusBadRequest)
			return
		}
		var req maintenanceRecordRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		rec, err := CreateMaintenanceRecord(r.Context(), pool, firmID, userID, assetID, CreateMaintenanceRecordInput{
			ScheduleID: req.ScheduleID, PerformedAt: req.PerformedAt, PerformedByPersonID: req.PerformedByPersonID,
			Description: req.Description, Cost: req.Cost, NextServiceDate: req.NextServiceDate,
		})
		if err != nil {
			writeAssetError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toMaintenanceRecordResponse(rec))
	}
}
