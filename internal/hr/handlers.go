// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package hr

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

// decodeJSONBody decodes r's body into v - identical contract to
// internal/workflow's own decodeJSONBody (a missing/empty body is a
// no-op, not an error), redeclared here rather than imported since it's
// an unexported helper in that package.
func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// RegisterRoutes wires the HR core's HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention as
// every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/people", auth(http.HandlerFunc(handleListPeople(pool))))
	mux.Handle("POST /api/firms/{firmID}/people", auth(http.HandlerFunc(handleCreatePerson(pool))))
	mux.Handle("GET /api/firms/{firmID}/people/{personID}", auth(http.HandlerFunc(handleGetPerson(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/people/{personID}", auth(http.HandlerFunc(handleUpdatePerson(pool))))
	mux.Handle("GET /api/firms/{firmID}/people/{personID}/contracts", auth(http.HandlerFunc(handleListContracts(pool))))
	mux.Handle("POST /api/firms/{firmID}/people/{personID}/contracts", auth(http.HandlerFunc(handleCreateContract(pool))))
}

// writeHRError maps this package's sentinel errors to the HTTP status
// that reflects why the caller isn't getting what they asked for - same
// convention as internal/accounting's writeAccountingError.
func writeHRError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrPersonNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidPerson), errors.Is(err, ErrInvalidContract):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type personResponse struct {
	ID           string         `json:"id"`
	FullName     string         `json:"fullName"`
	Type         string         `json:"type"`
	Email        *string        `json:"email,omitempty"`
	Phone        *string        `json:"phone,omitempty"`
	StartDate    *string        `json:"startDate,omitempty"`
	EndDate      *string        `json:"endDate,omitempty"`
	Status       string         `json:"status"`
	CustomFields map[string]any `json:"customFields"`
	CreatedAt    string         `json:"createdAt"`
}

func formatDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(dateLayout)
	return &s
}

func toPersonResponse(p Person) personResponse {
	return personResponse{
		ID:           p.ID.String(),
		FullName:     p.FullName,
		Type:         string(p.Type),
		Email:        p.Email,
		Phone:        p.Phone,
		StartDate:    formatDate(p.StartDate),
		EndDate:      formatDate(p.EndDate),
		Status:       string(p.Status),
		CustomFields: p.CustomFields,
		CreatedAt:    p.CreatedAt.Format(time.RFC3339),
	}
}

func handleListPeople(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := identity.FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		firmID, err := uuid.Parse(r.PathValue("firmID"))
		if err != nil {
			http.Error(w, "invalid firm id", http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		opts := ListPeopleOptions{Status: PersonStatus(r.URL.Query().Get("status")), Search: r.URL.Query().Get("q")}
		people, err := ListPeople(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writeHRError(w, err)
			return
		}

		resp := make([]personResponse, 0, len(people))
		for _, p := range people {
			resp = append(resp, toPersonResponse(p))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createPersonRequest struct {
	FullName     string         `json:"fullName"`
	Type         string         `json:"type"`
	Email        string         `json:"email"`
	Phone        string         `json:"phone"`
	StartDate    string         `json:"startDate"`
	EndDate      string         `json:"endDate"`
	CustomFields map[string]any `json:"customFields"`
}

func handleCreatePerson(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := identity.FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		firmID, err := uuid.Parse(r.PathValue("firmID"))
		if err != nil {
			http.Error(w, "invalid firm id", http.StatusBadRequest)
			return
		}
		var req createPersonRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		person, err := CreatePerson(r.Context(), pool, firmID, userID, CreatePersonInput{
			FullName: req.FullName, Type: PersonType(req.Type), Email: req.Email, Phone: req.Phone,
			StartDate: req.StartDate, EndDate: req.EndDate, CustomFields: req.CustomFields,
		})
		if err != nil {
			writeHRError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toPersonResponse(person))
	}
}

func handleGetPerson(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := identity.FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		firmID, err := uuid.Parse(r.PathValue("firmID"))
		if err != nil {
			http.Error(w, "invalid firm id", http.StatusBadRequest)
			return
		}
		personID, err := uuid.Parse(r.PathValue("personID"))
		if err != nil {
			http.Error(w, "invalid person id", http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		person, err := GetPerson(r.Context(), pool, firmID, userID, personID)
		if err != nil {
			writeHRError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toPersonResponse(person))
	}
}

type updatePersonRequest struct {
	FullName     *string        `json:"fullName"`
	Email        *string        `json:"email"`
	Phone        *string        `json:"phone"`
	StartDate    *string        `json:"startDate"`
	EndDate      *string        `json:"endDate"`
	Status       *string        `json:"status"`
	CustomFields map[string]any `json:"customFields"`
}

func handleUpdatePerson(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := identity.FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		firmID, err := uuid.Parse(r.PathValue("firmID"))
		if err != nil {
			http.Error(w, "invalid firm id", http.StatusBadRequest)
			return
		}
		personID, err := uuid.Parse(r.PathValue("personID"))
		if err != nil {
			http.Error(w, "invalid person id", http.StatusBadRequest)
			return
		}
		var req updatePersonRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		var status *PersonStatus
		if req.Status != nil {
			s := PersonStatus(*req.Status)
			status = &s
		}

		person, err := UpdatePerson(r.Context(), pool, firmID, userID, personID, PersonUpdate{
			FullName: req.FullName, Email: req.Email, Phone: req.Phone,
			StartDate: req.StartDate, EndDate: req.EndDate, Status: status, CustomFields: req.CustomFields,
		})
		if err != nil {
			writeHRError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toPersonResponse(person))
	}
}

type contractResponse struct {
	ID        string  `json:"id"`
	PersonID  string  `json:"personId"`
	Type      string  `json:"type"`
	Amount    *string `json:"amount,omitempty"`
	Currency  string  `json:"currency"`
	StartDate string  `json:"startDate"`
	EndDate   *string `json:"endDate,omitempty"`
	Notes     *string `json:"notes,omitempty"`
	CreatedAt string  `json:"createdAt"`
}

func toContractResponse(c Contract) contractResponse {
	startDate := c.StartDate
	return contractResponse{
		ID:        c.ID.String(),
		PersonID:  c.PersonID.String(),
		Type:      c.Type,
		Amount:    c.Amount,
		Currency:  c.Currency,
		StartDate: startDate.Format(dateLayout),
		EndDate:   formatDate(c.EndDate),
		Notes:     c.Notes,
		CreatedAt: c.CreatedAt.Format(time.RFC3339),
	}
}

func handleListContracts(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := identity.FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		firmID, err := uuid.Parse(r.PathValue("firmID"))
		if err != nil {
			http.Error(w, "invalid firm id", http.StatusBadRequest)
			return
		}
		personID, err := uuid.Parse(r.PathValue("personID"))
		if err != nil {
			http.Error(w, "invalid person id", http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		contracts, err := ListContracts(r.Context(), pool, firmID, userID, personID)
		if err != nil {
			writeHRError(w, err)
			return
		}

		resp := make([]contractResponse, 0, len(contracts))
		for _, c := range contracts {
			resp = append(resp, toContractResponse(c))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createContractRequest struct {
	Type      string `json:"type"`
	Amount    string `json:"amount"`
	Currency  string `json:"currency"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	Notes     string `json:"notes"`
}

func handleCreateContract(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := identity.FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		firmID, err := uuid.Parse(r.PathValue("firmID"))
		if err != nil {
			http.Error(w, "invalid firm id", http.StatusBadRequest)
			return
		}
		personID, err := uuid.Parse(r.PathValue("personID"))
		if err != nil {
			http.Error(w, "invalid person id", http.StatusBadRequest)
			return
		}
		var req createContractRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		contract, err := CreateContract(r.Context(), pool, firmID, userID, personID, CreateContractInput{
			Type: req.Type, Amount: req.Amount, Currency: req.Currency,
			StartDate: req.StartDate, EndDate: req.EndDate, Notes: req.Notes,
		})
		if err != nil {
			writeHRError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toContractResponse(contract))
	}
}
