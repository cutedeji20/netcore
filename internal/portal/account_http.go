package portal

import (
	"errors"
	"net/http"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/security"
)

// AccountHTTP exposes a signed-in customer's own plan and payment history.
// It deliberately has no customer, tenant, or user selector in the request.
type AccountHTTP struct{ service *AccountService }

func NewAccountHTTP(service *AccountService) (*AccountHTTP, error) {
	if service == nil {
		return nil, errors.New("portal: customer account service is required")
	}
	return &AccountHTTP{service: service}, nil
}

func (h *AccountHTTP) Routes(mux *http.ServeMux, sessions *auth.HTTP) error {
	if mux == nil || sessions == nil {
		return errors.New("portal: mux and session authentication are required")
	}
	mux.Handle("GET /api/v1/portal/account", sessions.RequireAuth(http.HandlerFunc(h.account)))
	return nil
}

func (h *AccountHTTP) account(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || principal.TenantID == "" || principal.UserID == "" {
		security.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication is required.")
		return
	}
	account, err := h.service.Account(r.Context(), principal.TenantID, principal.UserID)
	if errors.Is(err, ErrCustomerAccountNotFound) {
		security.WriteError(w, r, http.StatusNotFound, "CUSTOMER_ACCOUNT_NOT_FOUND", "A customer account was not found for this sign-in.")
		return
	}
	if err != nil {
		security.WriteError(w, r, http.StatusServiceUnavailable, "PORTAL_UNAVAILABLE", "Your account is temporarily unavailable. Please try again shortly.")
		return
	}
	response := customerAccountResponse{Data: customerAccountData{
		Subscriptions: make([]customerSubscriptionResponse, 0, len(account.Subscriptions)),
		Payments:      make([]customerPaymentResponse, 0, len(account.Payments)),
	}}
	for _, subscription := range account.Subscriptions {
		response.Data.Subscriptions = append(response.Data.Subscriptions, customerSubscriptionResponse{
			PlanName: subscription.PlanName, Status: subscription.Status, PaymentStatus: subscription.PaymentStatus,
			StartsAt: subscription.StartsAt, ExpiresAt: subscription.ExpiresAt,
		})
	}
	for _, payment := range account.Payments {
		response.Data.Payments = append(response.Data.Payments, customerPaymentResponse{
			Reference: payment.Reference, AmountMinor: payment.AmountMinor, Currency: payment.Currency,
			Status: payment.Status, CreatedAt: payment.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

type customerAccountResponse struct {
	Data customerAccountData `json:"data"`
}

type customerAccountData struct {
	Subscriptions []customerSubscriptionResponse `json:"subscriptions"`
	Payments      []customerPaymentResponse      `json:"payments"`
}

type customerSubscriptionResponse struct {
	PlanName      string     `json:"plan_name"`
	Status        string     `json:"status"`
	PaymentStatus string     `json:"payment_status"`
	StartsAt      *time.Time `json:"starts_at,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
}

type customerPaymentResponse struct {
	Reference   string    `json:"reference"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}
