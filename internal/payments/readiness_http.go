package payments

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/netcore-isp/netcore/internal/auth"
)

// ReadinessHTTP exposes only staff-safe payment deployment state. It never
// returns secret values, secret references, provider payloads, or webhooks.
type ReadinessHTTP struct {
	gateway     Gateway
	callbackURL string
}

func NewReadinessHTTP(gateway Gateway, callbackURL string) (*ReadinessHTTP, error) {
	if gateway == nil || strings.TrimSpace(gateway.Name()) == "" {
		return nil, errors.New("payments: readiness gateway is required")
	}
	callbackURL = strings.TrimSpace(callbackURL)
	if callbackURL != "" && !validPaymentCallbackURL(callbackURL) {
		return nil, errors.New("payments: readiness callback URL must be a valid HTTPS URL")
	}
	return &ReadinessHTTP{gateway: gateway, callbackURL: callbackURL}, nil
}

func (h *ReadinessHTTP) Routes(mux *http.ServeMux, sessions *auth.HTTP) error {
	if mux == nil || sessions == nil {
		return errors.New("payments: readiness mux and session authentication are required")
	}
	mux.Handle(
		"GET /api/v1/payments/readiness",
		sessions.RequireAuth(auth.RequirePermission("billing.read", http.HandlerFunc(h.get))),
	)
	return nil
}

type readinessResponse struct {
	Provider       string `json:"provider"`
	CheckoutStatus string `json:"checkout_status"`
	CallbackURL    string `json:"callback_url,omitempty"`
	WebhookURL     string `json:"webhook_url,omitempty"`
}

func (h *ReadinessHTTP) get(w http.ResponseWriter, r *http.Request) {
	response := readinessResponse{Provider: h.gateway.Name(), CheckoutStatus: "UNAVAILABLE"}
	if response.Provider == "disabled" {
		response.CheckoutStatus = "DISABLED"
		writeJSON(w, http.StatusOK, response)
		return
	}
	ready := h.gateway.Available()
	if probe, ok := h.gateway.(GatewayProbe); ok && ready {
		ready = probe.Check(r.Context()) == nil
	}
	if response.Provider == paystackName && ready && validPaymentCallbackURL(h.callbackURL) {
		response.CheckoutStatus = "READY"
		response.CallbackURL = h.callbackURL
		response.WebhookURL = webhookURL(h.callbackURL)
	}
	writeJSON(w, http.StatusOK, response)
}

func webhookURL(callbackURL string) string {
	parsed, err := url.Parse(callbackURL)
	if err != nil {
		return ""
	}
	parsed.Path = "/webhooks/" + paystackName
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}
