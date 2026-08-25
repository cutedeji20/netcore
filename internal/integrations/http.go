package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/logger"
	"github.com/netcore-isp/netcore/internal/security"
)

const maxConfigureBodyBytes = 20 * 1024

// HTTP exposes staff-only integration administration endpoints.
type HTTP struct{ service *Service }

func NewHTTP(service *Service) (*HTTP, error) {
	if service == nil {
		return nil, errors.New("integrations: service is required")
	}
	return &HTTP{service: service}, nil
}

func (h *HTTP) Routes(mux *http.ServeMux, sessions *auth.HTTP) error {
	if h == nil || h.service == nil || mux == nil || sessions == nil {
		return errors.New("integrations: mux, session authentication, and service are required")
	}
	mux.Handle("GET /api/v1/integrations", sessions.RequireAuth(auth.RequirePermission("integration.read", http.HandlerFunc(h.list))))
	mux.Handle("PUT /api/v1/integrations/resend", sessions.RequireAuth(auth.RequirePermission("integration.write", http.HandlerFunc(h.configureResend))))
	mux.Handle("PUT /api/v1/integrations/paystack", sessions.RequireAuth(auth.RequirePermission("integration.write", http.HandlerFunc(h.configurePaystack))))
	mux.Handle("POST /api/v1/integrations/resend/disable", sessions.RequireAuth(auth.RequirePermission("integration.write", h.disable(ProviderResend))))
	mux.Handle("POST /api/v1/integrations/paystack/disable", sessions.RequireAuth(auth.RequirePermission("integration.write", h.disable(ProviderPaystack))))
	mux.Handle("DELETE /api/v1/integrations/resend", sessions.RequireAuth(auth.RequirePermission("integration.write", h.disconnect(ProviderResend))))
	mux.Handle("DELETE /api/v1/integrations/paystack", sessions.RequireAuth(auth.RequirePermission("integration.write", h.disconnect(ProviderPaystack))))
	return nil
}

func (h *HTTP) list(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	if !principal.HasPermission("integration.read") {
		security.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "You do not have permission to view integrations.")
		return
	}
	snapshots, err := h.service.List(r.Context(), principal.TenantID)
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "INTEGRATIONS_UNAVAILABLE", "Integration settings are temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Integrations []Snapshot `json:"integrations"`
	}{Integrations: snapshots})
}

func (h *HTTP) configureResend(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	if !principal.HasPermission("integration.write") {
		security.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "You do not have permission to manage integrations.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxConfigureBodyBytes)
	defer r.Body.Close()
	var input struct {
		Credential  string `json:"credential"`
		SenderEmail string `json:"sender_email"`
		Password    string `json:"password"`
		MFACode     string `json:"mfa_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || strings.TrimSpace(input.Credential) == "" {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "A valid Resend configuration is required.")
		return
	}
	credential := []byte(input.Credential)
	defer clear(credential)
	err := h.service.Configure(r.Context(), ConfigureInput{Principal: principal, Password: input.Password, MFACode: input.MFACode, Provider: ProviderResend, Credential: credential, SenderEmail: input.SenderEmail})
	if h.writeConfigureError(w, r, err, "A valid Resend configuration is required.") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) configurePaystack(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	if !principal.HasPermission("integration.write") {
		security.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "You do not have permission to manage integrations.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxConfigureBodyBytes)
	defer r.Body.Close()
	var input struct {
		Credential string `json:"credential"`
		Mode       string `json:"mode"`
		Password   string `json:"password"`
		MFACode    string `json:"mfa_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || strings.TrimSpace(input.Credential) == "" {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "A valid Paystack configuration is required.")
		return
	}
	credential := []byte(input.Credential)
	defer clear(credential)
	err := h.service.Configure(r.Context(), ConfigureInput{Principal: principal, Password: input.Password, MFACode: input.MFACode, Provider: ProviderPaystack, Credential: credential, PaystackMode: input.Mode})
	if h.writeConfigureError(w, r, err, "A valid Paystack configuration is required.") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) writeConfigureError(w http.ResponseWriter, r *http.Request, err error, invalidMessage string) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrStepUpFailed) {
		security.WriteError(w, r, http.StatusUnauthorized, "STEP_UP_FAILED", "Password or authenticator code was not accepted.")
		return true
	}
	if errors.Is(err, ErrInvalidSettings) || errors.Is(err, ErrInvalidCredential) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", invalidMessage)
		return true
	}

	stage := "integration_service"
	switch {
	case errors.Is(err, ErrKeyUnavailable):
		stage = "key_vault_wrap"
	case errors.Is(err, ErrStorePrecondition):
		stage = "store_precondition"
	case errors.Is(err, ErrStoreUpsert):
		stage = "database_upsert"
	case errors.Is(err, ErrStoreAudit):
		stage = "audit_write"
	case errors.Is(err, ErrStoreTxSetup):
		stage = "database_transaction_setup"
	case errors.Is(err, ErrStoreTxCommit):
		stage = "database_transaction_commit"
	case errors.Is(err, ErrStoreUnavailable):
		stage = "database_save"
	}
	attributes := []any{slog.String("failure_stage", stage)}
	if stage == "audit_write" {
		attributes = append(attributes, slog.String("failure_cause", auditFailureCause(err)))
	}
	logger.FromContext(r.Context(), slog.Default()).Error("integration configuration failed", attributes...)
	security.WriteError(w, r, http.StatusServiceUnavailable, "INTEGRATIONS_UNAVAILABLE", "Integration settings are temporarily unavailable.")
	return true
}

// auditFailureCause emits only a fixed diagnostic category. It deliberately
// excludes PostgreSQL messages, constraints, driver details, and request data.
func auditFailureCause(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "context_cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "context_deadline"
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && validSQLState(databaseError.Code) {
		return "postgres_sqlstate_" + databaseError.Code
	}
	return "driver_or_transport"
}

func validSQLState(code string) bool {
	if len(code) != 5 {
		return false
	}
	for _, character := range code {
		if !((character >= '0' && character <= '9') || (character >= 'A' && character <= 'Z')) {
			return false
		}
	}
	return true
}

func (h *HTTP) disable(provider Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { h.changeProvider(w, r, provider, false) }
}

func (h *HTTP) disconnect(provider Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { h.changeProvider(w, r, provider, true) }
}

func (h *HTTP) changeProvider(w http.ResponseWriter, r *http.Request, provider Provider, disconnect bool) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	if !principal.HasPermission("integration.write") {
		security.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "You do not have permission to manage integrations.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxConfigureBodyBytes)
	defer r.Body.Close()
	var input struct {
		Password string `json:"password"`
		MFACode  string `json:"mfa_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Confirmation is required.")
		return
	}
	var err error
	if disconnect {
		err = h.service.Disconnect(r.Context(), principal, input.Password, input.MFACode, provider)
	} else {
		err = h.service.Disable(r.Context(), principal, input.Password, input.MFACode, provider)
	}
	if errors.Is(err, ErrStepUpFailed) {
		security.WriteError(w, r, http.StatusUnauthorized, "STEP_UP_FAILED", "Password or authenticator code was not accepted.")
		return
	}
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "INTEGRATIONS_UNAVAILABLE", "Integration settings are temporarily unavailable.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
