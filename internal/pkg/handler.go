package handler

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"zonary/internal/db"
	"zonary/internal/middleware"
	"zonary/internal/permission"
)

type UserActivityResponse struct {
	RecentActivities []Activity `json:"recent_activities"`
	Summary         Summary    `json:"summary"`
}

type Activity struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Details   string `json:"details"`
}

type Summary struct {
	TotalLogins    int `json:"total_logins"`
	ActionsToday   int `json:"actions_today"`
	LastActive     string `json:"last_active"`
}

func UserActivityDashboard(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		firmID := middleware.GetFirmID(ctx)

		if !permission.IsMember(pool, ctx, firmID, middleware.GetUserID(ctx)) {
			middleware.WriteError(w, http.StatusForbidden, "Access denied")
			return
		}

		// Mock data for demonstration
		response := UserActivityResponse{
			RecentActivities: []Activity{
				{ID: "1", Type: "login", Timestamp: "2024-01-15T10:30:00Z", Details: "User logged in"},
				{ID: "2", Type: "update", Timestamp: "2024-01-15T09:15:00Z", Details: "Profile updated"},
			},
			Summary: Summary{
				TotalLogins:  42,
				ActionsToday: 5,
				LastActive:   "2024-01-15T10:30:00Z",
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

// Deprecated: Use UserActivityDashboard instead
func GetDashboardWidget(pool *pgxpool.Pool) http.HandlerFunc {
	return UserActivityDashboard(pool)
}