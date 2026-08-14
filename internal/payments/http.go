package payments

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/security"
)

// HTTP exposes the customer-side payment boundary. A signed-in customer may
// start payment or ask the API to independently verify it. Neither endpoint
// accepts a price, currency, or a browser-declared success state.
type HTTP struct {
	service        *Service
	allowedOrigins []string
}

func NewHTTP(service *Service, allowedOrigins []string) (*HTTP, error) {
	if service == nil {
		return nil, errors.New("payments: service is required")
	}
	return &HTTP{service: service, allowedOrigins: slices.Clone(allowedOrigins)}, nil
}

func (h *HTTP) Routes(mux *http.ServeMux, sessions *auth.HTTP) error {
	if mux == nil || sessions == nil {
		return errors.New("payments: mux and session authentication are required")
	}
	mux.Handle("POST /api/v1/payments", sessions.RequireAuth(http.HandlerFunc(h.initiate)))
	mux.Handle("POST /api/v1/payments/{reference}/verify", sessions.RequireAuth(http.HandlerFunc(h.verify)))
	return nil
}

func (h *HTTP) initiate(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authorizedPrincipal(w, r)
	if !ok {
		return
	}
	var input struct {
		PlanID string `json:"plan_id"`
	}
	if err := decodeJSON(r, &input); err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Choose a valid internet plan.")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) {
		security.WriteError(w, r, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Please retry from the payment page.")
		return
	}
	checkout, err := h.service.Initiate(r.Context(), principal.TenantID, principal.UserID, input.PlanID, key)
	switch {
	case errors.Is(err, ErrInvalidRequest):
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Choose a valid internet plan.")
	case errors.Is(err, ErrPaymentNotFound):
		security.WriteNotFound(w, r)
	case errors.Is(err, ErrIdempotencyConflict):
		security.WriteError(w, r, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "This payment retry does not match the original request.")
	case errors.Is(err, ErrGatewayUnavailable):
		security.WriteError(w, r, http.StatusServiceUnavailable, "PAYMENTS_UNAVAILABLE", "Payments are temporarily unavailable. Please try again shortly.")
	case err != nil:
		security.WriteError(w, r, http.StatusServiceUnavailable, "PAYMENTS_UNAVAILABLE", "Payments are temporarily unavailable. Please try again shortly.")
	default:
		writeJSON(w, http.StatusCreated, checkout)
	}
}

func (h *HTTP) verify(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authorizedPrincipal(w, r)
	if !ok {
		return
	}
	result, err := h.service.Verify(r.Context(), principal.TenantID, principal.UserID, r.PathValue("reference"))
	switch {
	case errors.Is(err, ErrInvalidRequest):
		security.WriteNotFound(w, r)
	case errors.Is(err, ErrPaymentNotFound):
		security.WriteNotFound(w, r)
	case errors.Is(err, ErrPaymentNotPending), errors.Is(err, ErrGatewayRejected), errors.Is(err, ErrVerificationMismatch):
		security.WriteError(w, r, http.StatusConflict, "PAYMENT_NOT_VERIFIED", "We could not confirm this payment yet. Please check your payment and try again.")
	case errors.Is(err, ErrGatewayUnavailable):
		security.WriteError(w, r, http.StatusServiceUnavailable, "PAYMENTS_UNAVAILABLE", "We cannot confirm the payment right now. Please try again shortly.")
	case err != nil:
		security.WriteError(w, r, http.StatusServiceUnavailable, "PAYMENTS_UNAVAILABLE", "We cannot confirm the payment right now. Please try again shortly.")
	default:
		writeJSON(w, http.StatusOK, activationResponse{PaymentID: result.PaymentID, SubscriptionID: result.SubscriptionID, Status: result.Status, StartsAt: result.StartsAt, ExpiresAt: result.ExpiresAt, AlreadyActivated: result.AlreadyActivated})
	}
}

func (h *HTTP) authorizedPrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	if origin := r.Header.Get("Origin"); origin != "" && !slices.Contains(h.allowedOrigins, origin) {
		security.WriteError(w, r, http.StatusForbidden, "CSRF_REJECTED", "Request origin is not allowed.")
		return auth.Principal{}, false
	}
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || !validUUID(principal.TenantID) || !validUUID(principal.UserID) {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return auth.Principal{}, false
	}
	return principal, true
}

type activationResponse struct {
	PaymentID        string    `json:"payment_id"`
	SubscriptionID   string    `json:"subscription_id"`
	Status           string    `json:"status"`
	StartsAt         time.Time `json:"starts_at"`
	ExpiresAt        time.Time `json:"expires_at"`
	AlreadyActivated bool      `json:"already_activated"`
}

func decodeJSON(r *http.Request, destination any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("additional JSON values")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
