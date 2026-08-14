package payments

import (
	"errors"
	"io"
	"net/http"
)

// WebhookHTTP is intentionally independent from the customer payment routes:
// it has no session or Origin requirement. Its authentication is the raw-body
// provider signature, and it persists an acknowledgement before background
// processing can call the gateway.
type WebhookHTTP struct {
	gateway  WebhookGateway
	store    WebhookStore
	maxBytes int64
}

func NewWebhookHTTP(gateway WebhookGateway, store WebhookStore, maxBytes int64) (*WebhookHTTP, error) {
	if gateway == nil || store == nil || maxBytes < 1024 {
		return nil, errors.New("payments: webhook gateway, store, and body limit are required")
	}
	return &WebhookHTTP{gateway: gateway, store: store, maxBytes: maxBytes}, nil
}

func (h *WebhookHTTP) Routes(mux *http.ServeMux) error {
	if h == nil || mux == nil {
		return errors.New("payments: webhook mux is required")
	}
	mux.HandleFunc("POST /webhooks/"+h.gateway.Name(), h.receive)
	return nil
}

func (h *WebhookHTTP) receive(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	if err := h.gateway.VerifyWebhookSignature(r.Context(), raw, r.Header.Get("X-Paystack-Signature")); err != nil {
		if errors.Is(err, ErrWebhookInvalid) {
			http.Error(w, "invalid webhook signature", http.StatusUnauthorized)
			return
		}
		http.Error(w, "webhook unavailable", http.StatusServiceUnavailable)
		return
	}
	event, err := h.gateway.ParseWebhook(raw)
	if err != nil {
		http.Error(w, "invalid webhook payload", http.StatusBadRequest)
		return
	}
	receipt, err := NewWebhookReceipt(event, raw)
	if err != nil {
		http.Error(w, "invalid webhook payload", http.StatusBadRequest)
		return
	}
	result, err := h.store.RecordWebhook(r.Context(), receipt)
	if err != nil {
		http.Error(w, "webhook unavailable", http.StatusServiceUnavailable)
		return
	}
	// A mismatched duplicate is already recorded as a security audit event by
	// the store. Acknowledge it so a provider retry cannot manufacture a storm.
	_ = result
	w.WriteHeader(http.StatusOK)
}
