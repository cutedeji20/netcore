package workspace

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/security"
)

// HTTP exposes the read-only workspace settings snapshot.
type HTTP struct{ store Store }

func NewHTTP(store Store) (*HTTP, error) {
	if store == nil {
		return nil, errors.New("workspace: store is required")
	}
	return &HTTP{store: store}, nil
}

// Routes keeps profile changes, integration configuration, secrets, and
// payment-provider setup as separate audited workspace.write workflows.
func (h *HTTP) Routes(mux *http.ServeMux, sessions *auth.HTTP) error {
	if mux == nil || sessions == nil {
		return errors.New("workspace: mux and session authentication are required")
	}
	mux.Handle(
		"GET /api/v1/workspace/settings",
		sessions.RequireAuth(auth.RequirePermission("workspace.read", http.HandlerFunc(h.get))),
	)
	return nil
}

func (h *HTTP) get(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	snapshot, err := h.store.Get(r.Context(), principal.TenantID)
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "WORKSPACE_UNAVAILABLE", "Workspace settings are temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, toResponseSnapshot(snapshot))
}

// responseSnapshot has no provider credentials, API endpoints, secret paths,
// billing account references, router IPs, or internal tenant database IDs.
type responseSnapshot struct {
	Name              string    `json:"name"`
	Slug              string    `json:"slug"`
	Timezone          string    `json:"timezone"`
	Currency          string    `json:"currency"`
	Status            string    `json:"status"`
	UpdatedAt         time.Time `json:"updated_at"`
	RegisteredRouters int       `json:"registered_routers"`
	ActiveTeamMembers int       `json:"active_team_members"`
}

func toResponseSnapshot(snapshot Snapshot) responseSnapshot {
	return responseSnapshot{
		Name:              snapshot.Name,
		Slug:              snapshot.Slug,
		Timezone:          snapshot.Timezone,
		Currency:          snapshot.Currency,
		Status:            snapshot.Status,
		UpdatedAt:         snapshot.UpdatedAt,
		RegisteredRouters: snapshot.RegisteredRouters,
		ActiveTeamMembers: snapshot.ActiveTeamMembers,
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
