// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package wizard

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

// RegisterRoutes wires the wizard's HTTP endpoints into mux, protected by
// verifier's bearer-token check like every other route group. Deliberately
// not under /api/firms/{firmID}/... (internal/workflow's convention):
// there is no firm yet at this point - see internal/identity's own
// /api/me, the other endpoint reachable before any firm context exists.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/wizard/nodes/{nodeKey}", auth(http.HandlerFunc(handleGetNode(pool))))
	mux.Handle("POST /api/wizard/nodes/{nodeKey}/answer", auth(http.HandlerFunc(handleAnswerNode(pool))))
}

type answerResponse struct {
	Value    string `json:"value"`
	LabelKey string `json:"labelKey"`
}

type nodeResponse struct {
	Key            string             `json:"key"`
	Kind           string             `json:"kind"`
	QuestionKey    string             `json:"questionKey,omitempty"`
	Answers        []answerResponse   `json:"answers,omitempty"`
	PlaceholderKey string             `json:"placeholderKey,omitempty"`
	Result         *firmResultPayload `json:"result,omitempty"`
}

type firmResultPayload struct {
	FirmID   string `json:"firmId"`
	FirmName string `json:"firmName"`
}

func kindString(k NodeKind) string {
	switch k {
	case NodeQuestion:
		return "question"
	case NodeAction:
		return "action"
	case NodePlaceholder:
		return "placeholder"
	default:
		return "unknown"
	}
}

func toNodeResponse(n *Node) nodeResponse {
	resp := nodeResponse{
		Key:            n.Key,
		Kind:           kindString(n.Kind),
		QuestionKey:    n.QuestionKey,
		PlaceholderKey: n.PlaceholderKey,
	}
	for _, a := range n.Answers {
		resp.Answers = append(resp.Answers, answerResponse{Value: a.Value, LabelKey: a.LabelKey})
	}
	return resp
}

func writeNodeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNodeNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotQuestion), errors.Is(err, ErrUnknownAnswer):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func handleGetNode(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := identity.FromContext(r.Context()); !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}

		node, err := Lookup(r.PathValue("nodeKey"))
		if err != nil {
			writeNodeError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toNodeResponse(node))
	}
}

type answerRequest struct {
	Answer   string `json:"answer"`
	FirmName string `json:"firmName"`
}

// handleAnswerNode resolves the node reached by submitting an answer at
// the given question node. If that lands on the create-default-firm
// action, it runs CreateDefaultFirm and returns the result inline rather
// than requiring a second round trip - the wizard has nothing left to ask
// once its one implemented branch resolves.
func handleAnswerNode(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := identity.FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}

		node, err := Lookup(r.PathValue("nodeKey"))
		if err != nil {
			writeNodeError(w, err)
			return
		}

		var req answerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		next, err := node.Answer(req.Answer)
		if err != nil {
			writeNodeError(w, err)
			return
		}

		resp := toNodeResponse(next)

		if next.Kind == NodeAction && next.ActionKey == ActionCreateDefaultFirm {
			userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
			if err != nil {
				http.Error(w, "failed to resolve user", http.StatusInternalServerError)
				return
			}

			result, err := CreateDefaultFirm(r.Context(), pool, userID, req.FirmName)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			resp.Result = &firmResultPayload{
				FirmID:   result.FirmID.String(),
				FirmName: result.FirmName,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
