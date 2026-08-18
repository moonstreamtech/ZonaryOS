// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package integration

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/cryptutil"
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

// RegisterRoutes wires this package's HTTP endpoints into mux - the
// ordinary Keycloak bearer-token chain, same convention as every other
// firm-scoped route group in this codebase. enc may be nil
// (ZONARYOS_INTEGRATION_ENCRYPTION_KEY unset); every handler that needs
// it passes it straight through and fails clearly (ErrEncryptionKeyNotSet,
// mapped to 500) only if a connector's config actually contains a
// sensitive field - see crypto.go's own doc comment.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool, enc *cryptutil.Encryptor) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/integration-connectors", auth(http.HandlerFunc(handleListConnectors(pool, enc))))
	mux.Handle("POST /api/firms/{firmID}/integration-connectors", auth(http.HandlerFunc(handleCreateConnector(pool, enc))))
	mux.Handle("PATCH /api/firms/{firmID}/integration-connectors/{connectorID}", auth(http.HandlerFunc(handleUpdateConnector(pool, enc))))
	mux.Handle("DELETE /api/firms/{firmID}/integration-connectors/{connectorID}", auth(http.HandlerFunc(handleDeleteConnector(pool))))
	mux.Handle("POST /api/firms/{firmID}/integration-connectors/{connectorID}/test", auth(http.HandlerFunc(handleTestConnection(pool, enc))))

	mux.Handle("GET /api/firms/{firmID}/integration-mappings", auth(http.HandlerFunc(handleListMappings(pool))))
	mux.Handle("POST /api/firms/{firmID}/integration-mappings", auth(http.HandlerFunc(handleCreateMapping(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/integration-mappings/{mappingID}", auth(http.HandlerFunc(handleUpdateMapping(pool))))
	mux.Handle("DELETE /api/firms/{firmID}/integration-mappings/{mappingID}", auth(http.HandlerFunc(handleDeleteMapping(pool))))

	mux.Handle("GET /api/firms/{firmID}/integration-sync-logs", auth(http.HandlerFunc(handleListSyncLogs(pool))))
}

func writeIntegrationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrConnectorNotFound), errors.Is(err, ErrMappingNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidConnector), errors.Is(err, ErrInvalidMapping), errors.Is(err, ErrInvalidTransform):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrEncryptionKeyNotSet):
		http.Error(w, err.Error(), http.StatusInternalServerError)
	case errors.Is(err, ErrConnectionTestNotImplemented):
		http.Error(w, err.Error(), http.StatusNotImplemented)
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

type connectorResponse struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Type           string         `json:"type"`
	Config         map[string]any `json:"config"`
	IsActive       bool           `json:"isActive"`
	LastSyncAt     *string        `json:"lastSyncAt,omitempty"`
	LastSyncStatus *string        `json:"lastSyncStatus,omitempty"`
	CreatedAt      string         `json:"createdAt"`
}

func formatTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
}

func toConnectorResponse(c Connector) connectorResponse {
	return connectorResponse{
		ID: c.ID.String(), Name: c.Name, Type: string(c.Type), Config: c.Config, IsActive: c.IsActive,
		LastSyncAt: formatTime(c.LastSyncAt), LastSyncStatus: c.LastSyncStatus, CreatedAt: c.CreatedAt.Format(time.RFC3339),
	}
}

type connectorRequest struct {
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Config   map[string]any `json:"config"`
	IsActive bool           `json:"isActive"`
}

func (req connectorRequest) toInput() ConnectorInput {
	return ConnectorInput{Name: req.Name, Type: ConnectorType(req.Type), Config: req.Config, IsActive: req.IsActive}
}

func handleListConnectors(pool *pgxpool.Pool, enc *cryptutil.Encryptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		connectors, err := ListConnectors(r.Context(), pool, enc, firmID, userID)
		if err != nil {
			writeIntegrationError(w, err)
			return
		}
		resp := make([]connectorResponse, 0, len(connectors))
		for _, c := range connectors {
			resp = append(resp, toConnectorResponse(c))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleCreateConnector(pool *pgxpool.Pool, enc *cryptutil.Encryptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req connectorRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		connector, err := CreateConnector(r.Context(), pool, enc, firmID, userID, req.toInput())
		if err != nil {
			writeIntegrationError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toConnectorResponse(connector))
	}
}

func handleUpdateConnector(pool *pgxpool.Pool, enc *cryptutil.Encryptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		connectorID, err := uuid.Parse(r.PathValue("connectorID"))
		if err != nil {
			http.Error(w, "invalid connector id", http.StatusBadRequest)
			return
		}
		var req connectorRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		connector, err := UpdateConnector(r.Context(), pool, enc, firmID, userID, connectorID, req.toInput())
		if err != nil {
			writeIntegrationError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toConnectorResponse(connector))
	}
}

func handleDeleteConnector(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		connectorID, err := uuid.Parse(r.PathValue("connectorID"))
		if err != nil {
			http.Error(w, "invalid connector id", http.StatusBadRequest)
			return
		}
		if err := DeleteConnector(r.Context(), pool, firmID, userID, connectorID); err != nil {
			writeIntegrationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleTestConnection(pool *pgxpool.Pool, enc *cryptutil.Encryptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		connectorID, err := uuid.Parse(r.PathValue("connectorID"))
		if err != nil {
			http.Error(w, "invalid connector id", http.StatusBadRequest)
			return
		}
		if err := TestConnection(r.Context(), pool, enc, firmID, userID, connectorID); err != nil {
			writeIntegrationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type mappingResponse struct {
	ID            string         `json:"id"`
	ConnectorID   string         `json:"connectorId"`
	SourceEntity  string         `json:"sourceEntity"`
	TargetEntity  string         `json:"targetEntity"`
	FieldMappings []FieldMapping `json:"fieldMappings"`
	SyncDirection string         `json:"syncDirection"`
	IsActive      bool           `json:"isActive"`
	CreatedAt     string         `json:"createdAt"`
}

func toMappingResponse(m Mapping) mappingResponse {
	return mappingResponse{
		ID: m.ID.String(), ConnectorID: m.ConnectorID.String(), SourceEntity: m.SourceEntity, TargetEntity: m.TargetEntity,
		FieldMappings: m.FieldMappings, SyncDirection: string(m.SyncDirection), IsActive: m.IsActive,
		CreatedAt: m.CreatedAt.Format(time.RFC3339),
	}
}

type mappingRequest struct {
	ConnectorID   string         `json:"connectorId"`
	SourceEntity  string         `json:"sourceEntity"`
	TargetEntity  string         `json:"targetEntity"`
	FieldMappings []FieldMapping `json:"fieldMappings"`
	SyncDirection string         `json:"syncDirection"`
	IsActive      bool           `json:"isActive"`
}

func (req mappingRequest) toInput() (MappingInput, error) {
	connectorID, err := uuid.Parse(req.ConnectorID)
	if err != nil {
		return MappingInput{}, err
	}
	return MappingInput{
		ConnectorID: connectorID, SourceEntity: req.SourceEntity, TargetEntity: req.TargetEntity,
		FieldMappings: req.FieldMappings, SyncDirection: SyncDirection(req.SyncDirection), IsActive: req.IsActive,
	}, nil
}

func handleListMappings(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var connectorID uuid.UUID
		if raw := r.URL.Query().Get("connectorId"); raw != "" {
			id, err := uuid.Parse(raw)
			if err != nil {
				http.Error(w, "invalid connectorId", http.StatusBadRequest)
				return
			}
			connectorID = id
		}
		mappings, err := ListMappings(r.Context(), pool, firmID, userID, connectorID)
		if err != nil {
			writeIntegrationError(w, err)
			return
		}
		resp := make([]mappingResponse, 0, len(mappings))
		for _, m := range mappings {
			resp = append(resp, toMappingResponse(m))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleCreateMapping(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req mappingRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		input, err := req.toInput()
		if err != nil {
			http.Error(w, "invalid connectorId", http.StatusBadRequest)
			return
		}
		mapping, err := CreateMapping(r.Context(), pool, firmID, userID, input)
		if err != nil {
			writeIntegrationError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toMappingResponse(mapping))
	}
}

func handleUpdateMapping(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		mappingID, err := uuid.Parse(r.PathValue("mappingID"))
		if err != nil {
			http.Error(w, "invalid mapping id", http.StatusBadRequest)
			return
		}
		var req mappingRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		input, err := req.toInput()
		if err != nil {
			http.Error(w, "invalid connectorId", http.StatusBadRequest)
			return
		}
		mapping, err := UpdateMapping(r.Context(), pool, firmID, userID, mappingID, input)
		if err != nil {
			writeIntegrationError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toMappingResponse(mapping))
	}
}

func handleDeleteMapping(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		mappingID, err := uuid.Parse(r.PathValue("mappingID"))
		if err != nil {
			http.Error(w, "invalid mapping id", http.StatusBadRequest)
			return
		}
		if err := DeleteMapping(r.Context(), pool, firmID, userID, mappingID); err != nil {
			writeIntegrationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type syncLogResponse struct {
	ID               string   `json:"id"`
	ConnectorID      string   `json:"connectorId"`
	MappingID        *string  `json:"mappingId,omitempty"`
	StartedAt        string   `json:"startedAt"`
	FinishedAt       *string  `json:"finishedAt,omitempty"`
	RecordsProcessed int      `json:"recordsProcessed"`
	RecordsFailed    int      `json:"recordsFailed"`
	Status           string   `json:"status"`
	ErrorLog         []string `json:"errorLog,omitempty"`
}

func toSyncLogResponse(l SyncLog) syncLogResponse {
	resp := syncLogResponse{
		ID: l.ID.String(), ConnectorID: l.ConnectorID.String(), StartedAt: l.StartedAt.Format(time.RFC3339),
		FinishedAt: formatTime(l.FinishedAt), RecordsProcessed: l.RecordsProcessed, RecordsFailed: l.RecordsFailed,
		Status: string(l.Status), ErrorLog: l.ErrorLog,
	}
	if l.MappingID != nil {
		s := l.MappingID.String()
		resp.MappingID = &s
	}
	return resp
}

func handleListSyncLogs(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var connectorID uuid.UUID
		if raw := r.URL.Query().Get("connectorId"); raw != "" {
			id, err := uuid.Parse(raw)
			if err != nil {
				http.Error(w, "invalid connectorId", http.StatusBadRequest)
				return
			}
			connectorID = id
		}
		logs, err := ListSyncLogs(r.Context(), pool, firmID, userID, connectorID)
		if err != nil {
			writeIntegrationError(w, err)
			return
		}
		resp := make([]syncLogResponse, 0, len(logs))
		for _, l := range logs {
			resp = append(resp, toSyncLogResponse(l))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
