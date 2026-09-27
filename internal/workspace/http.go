package workspace

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/security"
)

// HTTP exposes the read-only workspace settings snapshot.
type HTTP struct {
	store        Store
	reset        *TestDataResetService
	resetEnabled bool
}

func NewHTTP(store Store) (*HTTP, error) {
	if store == nil {
		return nil, errors.New("workspace: store is required")
	}
	return &HTTP{store: store}, nil
}

// ConfigureTestDataReset wires the deliberately opt-in pre-live reset control.
// It remains disabled unless production configuration explicitly enables it.
func (h *HTTP) ConfigureTestDataReset(service *TestDataResetService, enabled bool) {
	if h == nil {
		return
	}
	h.reset = service
	h.resetEnabled = enabled && service != nil
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
	mux.Handle("PUT /api/v1/workspace/verification-policy", sessions.RequireAuth(sessions.RequireAllowedOrigin(auth.RequirePermission("workspace.write", http.HandlerFunc(h.setVerificationPolicy)))))
	mux.Handle("GET /api/v1/workspace/test-data-reset/preview", sessions.RequireAuth(auth.RequirePermission("tenant.test_data_reset", http.HandlerFunc(h.previewTestDataReset))))
	mux.Handle("POST /api/v1/workspace/test-data-reset", sessions.RequireAuth(sessions.RequireAllowedOrigin(auth.RequirePermission("tenant.test_data_reset", http.HandlerFunc(h.executeTestDataReset)))))
	return nil
}

func (h *HTTP) previewTestDataReset(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok || h.reset == nil || !h.resetEnabled {
		security.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "The requested resource was not found.")
		return
	}
	preview, err := h.reset.Preview(r.Context(), p)
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "RESET_UNAVAILABLE", "Test-data reset preview is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Data TestDataResetPreview `json:"data"`
	}{Data: preview})
}

func (h *HTTP) executeTestDataReset(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok || h.reset == nil || !h.resetEnabled {
		security.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "The requested resource was not found.")
		return
	}
	var in struct {
		Password        string `json:"password"`
		MFACode         string `json:"mfa_code"`
		Confirmation    string `json:"confirmation"`
		BackupReference string `json:"backup_reference"`
		Reason          string `json:"reason"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil || d.Decode(&struct{}{}) != io.EOF {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Reset confirmation is invalid.")
		return
	}
	snapshot, err := h.store.Get(r.Context(), p.TenantID)
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "RESET_UNAVAILABLE", "Test-data reset is temporarily unavailable.")
		return
	}
	result, err := h.reset.Execute(r.Context(), TestDataResetInput{Principal: p, Password: in.Password, MFACode: in.MFACode, Confirmation: in.Confirmation, BackupReference: in.BackupReference, Reason: in.Reason}, snapshot.Slug)
	if errors.Is(err, ErrStepUpFailed) {
		security.WriteError(w, r, http.StatusUnauthorized, "STEP_UP_FAILED", "Password or authenticator code was not accepted.")
		return
	}
	if errors.Is(err, ErrResetBlocked) {
		security.WriteError(w, r, http.StatusConflict, "RESET_BLOCKED", "Close active sessions and wait for queued tenant events before resetting test data.")
		return
	}
	if errors.Is(err, ErrInvalidReset) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "A backup reference, reason, and exact reset confirmation are required.")
		return
	}
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "RESET_UNAVAILABLE", "Test-data reset could not be completed.")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Data TestDataResetResult `json:"data"`
	}{Data: result})
}

func (h *HTTP) setVerificationPolicy(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok || p.TenantID == "" || p.UserID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	var in struct {
		RequireEmailVerification bool `json:"require_email_verification"`
		RequirePhoneVerification bool `json:"require_phone_verification"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Verification policy is invalid.")
		return
	}
	if err := d.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Verification policy is invalid.")
		return
	}
	snapshot, err := h.store.SetVerificationPolicy(r.Context(), p.TenantID, p.UserID, in.RequireEmailVerification, in.RequirePhoneVerification)
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "WORKSPACE_UNAVAILABLE", "Workspace settings are temporarily unavailable.")
		return
	}
	response := toResponseSnapshot(snapshot)
	response.TestDataResetEnabled = h.resetEnabled
	writeJSON(w, http.StatusOK, response)
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
	response := toResponseSnapshot(snapshot)
	response.TestDataResetEnabled = h.resetEnabled
	writeJSON(w, http.StatusOK, response)
}

// responseSnapshot has no provider credentials, API endpoints, secret paths,
// billing account references, router IPs, or internal tenant database IDs.
type responseSnapshot struct {
	Name                     string    `json:"name"`
	Slug                     string    `json:"slug"`
	Timezone                 string    `json:"timezone"`
	Currency                 string    `json:"currency"`
	Status                   string    `json:"status"`
	UpdatedAt                time.Time `json:"updated_at"`
	RegisteredRouters        int       `json:"registered_routers"`
	ActiveTeamMembers        int       `json:"active_team_members"`
	RequireEmailVerification bool      `json:"require_email_verification"`
	RequirePhoneVerification bool      `json:"require_phone_verification"`
	TestDataResetEnabled     bool      `json:"test_data_reset_enabled"`
}

func toResponseSnapshot(snapshot Snapshot) responseSnapshot {
	return responseSnapshot{
		Name:                     snapshot.Name,
		Slug:                     snapshot.Slug,
		Timezone:                 snapshot.Timezone,
		Currency:                 snapshot.Currency,
		Status:                   snapshot.Status,
		UpdatedAt:                snapshot.UpdatedAt,
		RegisteredRouters:        snapshot.RegisteredRouters,
		ActiveTeamMembers:        snapshot.ActiveTeamMembers,
		RequireEmailVerification: snapshot.RequireEmailVerification,
		RequirePhoneVerification: snapshot.RequirePhoneVerification,
		TestDataResetEnabled:     snapshot.TestDataResetEnabled,
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
