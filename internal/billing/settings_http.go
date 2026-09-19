package billing

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/security"
)

type SettingsHTTP struct {
	service  *SettingsService
	clientIP func(*http.Request) string
}

func NewSettingsHTTP(service *SettingsService) (*SettingsHTTP, error) {
	if service == nil {
		return nil, ErrUnavailable
	}
	return &SettingsHTTP{service: service}, nil
}
func (h *SettingsHTTP) Routes(mux *http.ServeMux, sessions *auth.HTTP) error {
	if mux == nil || sessions == nil {
		return errors.New("billing: mux and session authentication are required")
	}
	mux.Handle("GET /api/v1/billing/settings", sessions.RequireAuth(auth.RequirePermission("billing.read", http.HandlerFunc(h.get))))
	mux.Handle("PUT /api/v1/billing/settings", sessions.RequireAuth(auth.RequirePermission("billing.write", http.HandlerFunc(h.put))))
	h.clientIP = sessions.ClientIP
	return nil
}
func (h *SettingsHTTP) get(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok || p.TenantID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	settings, err := h.service.Get(r.Context(), p.TenantID)
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "BILLING_UNAVAILABLE", "Billing settings are temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Data struct {
			FixedBankChargeMinor int64  `json:"fixed_bank_charge_minor"`
			Currency             string `json:"currency"`
			UpdatedAt            any    `json:"updated_at"`
		} `json:"data"`
	}{Data: struct {
		FixedBankChargeMinor int64  `json:"fixed_bank_charge_minor"`
		Currency             string `json:"currency"`
		UpdatedAt            any    `json:"updated_at"`
	}{settings.FixedBankChargeMinor, settings.Currency, settings.UpdatedAt}})
}
func (h *SettingsHTTP) put(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.PrincipalFromContext(r.Context())
	if !ok || p.TenantID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	if !p.HasPermission("billing.write") {
		security.WriteError(w, r, http.StatusForbidden, "FORBIDDEN", "You do not have permission to change billing settings.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	var in struct {
		FixedBankCharge string `json:"fixed_bank_charge"`
		Password        string `json:"password"`
		MFACode         string `json:"mfa_code"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil || d.Decode(&struct{}{}) != io.EOF {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "An exact NGN bank charge and administrator confirmation are required.")
		return
	}
	ip := ""
	if h.clientIP != nil {
		ip = h.clientIP(r)
	}
	settings, err := h.service.Update(r.Context(), p, MutationActor{IP: ip, UserAgent: r.UserAgent()}, in.FixedBankCharge, in.Password, in.MFACode)
	if errors.Is(err, ErrStepUpFailed) {
		security.WriteError(w, r, http.StatusUnauthorized, "STEP_UP_FAILED", "Password or authenticator code was not accepted.")
		return
	}
	if errors.Is(err, ErrInvalidClear) {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Bank charge must be a non-negative exact NGN amount.")
		return
	}
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "BILLING_UNAVAILABLE", "Billing settings could not be saved.")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Data struct {
			FixedBankChargeMinor int64  `json:"fixed_bank_charge_minor"`
			Currency             string `json:"currency"`
		} `json:"data"`
	}{Data: struct {
		FixedBankChargeMinor int64  `json:"fixed_bank_charge_minor"`
		Currency             string `json:"currency"`
	}{settings.FixedBankChargeMinor, settings.Currency}})
}
