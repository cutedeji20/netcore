package payments

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/netcore-isp/netcore/internal/security"
)

func (h *HTTP) voucherQuote(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authorizedPrincipal(w, r)
	if !ok {
		return
	}
	quantity, err := strconv.Atoi(r.URL.Query().Get("quantity"))
	if err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Quantity must be between 1 and 100.")
		return
	}
	quote, err := h.service.QuoteVoucherOrder(r.Context(), p.TenantID, p.UserID, r.URL.Query().Get("plan_id"), quantity)
	switch {
	case errors.Is(err, ErrInvalidRequest):
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Choose a plan and quantity between 1 and 100.")
	case errors.Is(err, ErrPaymentNotFound):
		security.WriteNotFound(w, r)
	case err != nil:
		security.WriteError(w, r, http.StatusServiceUnavailable, "PAYMENTS_UNAVAILABLE", "A quote is temporarily unavailable.")
	default:
		writeJSON(w, http.StatusOK, struct {
			Data VoucherQuote `json:"data"`
		}{quote})
	}
}

func (h *HTTP) initiateVoucherOrder(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authorizedPrincipal(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var in struct {
		PlanID   string `json:"plan_id"`
		Quantity int    `json:"quantity"`
	}
	if err := decodeJSON(r, &in); err != nil {
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Choose a plan and quantity between 1 and 100.")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	checkout, err := h.service.InitiateVoucherOrder(r.Context(), p.TenantID, p.UserID, in.PlanID, in.Quantity, key)
	switch {
	case errors.Is(err, ErrInvalidRequest):
		security.WriteError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Choose a plan and quantity between 1 and 100.")
	case errors.Is(err, ErrPaymentNotFound):
		security.WriteNotFound(w, r)
	case errors.Is(err, ErrIdempotencyConflict):
		security.WriteError(w, r, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "This checkout retry does not match its original request.")
	case err != nil:
		security.WriteError(w, r, http.StatusServiceUnavailable, "PAYMENTS_UNAVAILABLE", "Checkout is temporarily unavailable.")
	default:
		writeJSON(w, http.StatusCreated, checkout)
	}
}

func (h *HTTP) voucherOrder(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authorizedPrincipal(w, r)
	if !ok {
		return
	}
	order, err := h.service.VoucherOrder(r.Context(), p.TenantID, p.UserID, r.PathValue("id"))
	if err != nil {
		security.WriteNotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Data VoucherOrder `json:"data"`
	}{order})
}
