// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package logistics

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

// decodeJSONBody decodes r's body into v - same contract as every other
// package's own copy (a missing/empty body is a no-op, not an error).
func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// RegisterRoutes wires logistics' HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention as
// every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/deliveries", auth(http.HandlerFunc(handleListDeliveries(pool))))
	mux.Handle("POST /api/firms/{firmID}/deliveries", auth(http.HandlerFunc(handleCreateDelivery(pool))))
	mux.Handle("GET /api/firms/{firmID}/deliveries/{deliveryID}", auth(http.HandlerFunc(handleGetDelivery(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/deliveries/{deliveryID}", auth(http.HandlerFunc(handleUpdateDelivery(pool))))
}

// writeLogisticsError maps this package's sentinel errors to the HTTP
// status that reflects why the caller isn't getting what they asked for -
// same convention as internal/inventory's writeInventoryError.
func writeLogisticsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrDeliveryNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidDelivery):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type deliveryResponse struct {
	ID                 string         `json:"id"`
	Reference          *string        `json:"reference,omitempty"`
	OriginAddress      *string        `json:"originAddress,omitempty"`
	DestinationAddress *string        `json:"destinationAddress,omitempty"`
	Carrier            *string        `json:"carrier,omitempty"`
	TrackingNumber     *string        `json:"trackingNumber,omitempty"`
	Status             string         `json:"status"`
	EstimatedDate      *string        `json:"estimatedDate,omitempty"`
	ActualDate         *string        `json:"actualDate,omitempty"`
	SourceType         string         `json:"sourceType,omitempty"`
	SourceID           *string        `json:"sourceId,omitempty"`
	CustomFields       map[string]any `json:"customFields"`
	CreatedAt          string         `json:"createdAt"`
}

func formatDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(dateLayout)
	return &s
}

func toDeliveryResponse(d Delivery) deliveryResponse {
	var sourceID *string
	if d.SourceID != nil {
		s := d.SourceID.String()
		sourceID = &s
	}
	return deliveryResponse{
		ID: d.ID.String(), Reference: d.Reference, OriginAddress: d.OriginAddress, DestinationAddress: d.DestinationAddress,
		Carrier: d.Carrier, TrackingNumber: d.TrackingNumber, Status: string(d.Status),
		EstimatedDate: formatDate(d.EstimatedDate), ActualDate: formatDate(d.ActualDate),
		SourceType: d.SourceType, SourceID: sourceID, CustomFields: d.CustomFields, CreatedAt: d.CreatedAt.Format(time.RFC3339),
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

func handleListDeliveries(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		opts := ListDeliveriesOptions{Status: DeliveryStatus(r.URL.Query().Get("status"))}
		deliveries, err := ListDeliveries(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writeLogisticsError(w, err)
			return
		}

		resp := make([]deliveryResponse, 0, len(deliveries))
		for _, d := range deliveries {
			resp = append(resp, toDeliveryResponse(d))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createDeliveryRequest struct {
	Reference          string         `json:"reference"`
	OriginAddress      string         `json:"originAddress"`
	DestinationAddress string         `json:"destinationAddress"`
	Carrier            string         `json:"carrier"`
	TrackingNumber     string         `json:"trackingNumber"`
	EstimatedDate      string         `json:"estimatedDate"`
	CustomFields       map[string]any `json:"customFields"`
}

func handleCreateDelivery(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createDeliveryRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		delivery, err := CreateDelivery(r.Context(), pool, firmID, userID, CreateDeliveryInput{
			Reference: req.Reference, OriginAddress: req.OriginAddress, DestinationAddress: req.DestinationAddress,
			Carrier: req.Carrier, TrackingNumber: req.TrackingNumber, EstimatedDate: req.EstimatedDate, CustomFields: req.CustomFields,
		})
		if err != nil {
			writeLogisticsError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toDeliveryResponse(delivery))
	}
}

func handleGetDelivery(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		deliveryID, err := uuid.Parse(r.PathValue("deliveryID"))
		if err != nil {
			http.Error(w, "invalid delivery id", http.StatusBadRequest)
			return
		}

		delivery, err := GetDelivery(r.Context(), pool, firmID, userID, deliveryID)
		if err != nil {
			writeLogisticsError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toDeliveryResponse(delivery))
	}
}

type updateDeliveryRequest struct {
	Reference          *string        `json:"reference"`
	OriginAddress      *string        `json:"originAddress"`
	DestinationAddress *string        `json:"destinationAddress"`
	Carrier            *string        `json:"carrier"`
	TrackingNumber     *string        `json:"trackingNumber"`
	Status             *string        `json:"status"`
	EstimatedDate      *string        `json:"estimatedDate"`
	ActualDate         *string        `json:"actualDate"`
	CustomFields       map[string]any `json:"customFields"`
}

func handleUpdateDelivery(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		deliveryID, err := uuid.Parse(r.PathValue("deliveryID"))
		if err != nil {
			http.Error(w, "invalid delivery id", http.StatusBadRequest)
			return
		}
		var req updateDeliveryRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		var statusUpdate *DeliveryStatus
		if req.Status != nil {
			s := DeliveryStatus(*req.Status)
			statusUpdate = &s
		}

		delivery, err := UpdateDelivery(r.Context(), pool, firmID, userID, deliveryID, DeliveryUpdate{
			Reference: req.Reference, OriginAddress: req.OriginAddress, DestinationAddress: req.DestinationAddress,
			Carrier: req.Carrier, TrackingNumber: req.TrackingNumber, Status: statusUpdate,
			EstimatedDate: req.EstimatedDate, ActualDate: req.ActualDate, CustomFields: req.CustomFields,
		})
		if err != nil {
			writeLogisticsError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toDeliveryResponse(delivery))
	}
}
