package payments

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPaymentReadinessReportsDisabledGatewayWithoutSensitiveConfiguration(t *testing.T) {
	h, err := NewReadinessHTTP(NewDisabledGateway(), "")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	h.get(response, httptest.NewRequest(http.MethodGet, "/api/v1/payments/readiness", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var body readinessResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Provider != "disabled" || body.CheckoutStatus != "DISABLED" || body.CallbackURL != "" || body.WebhookURL != "" {
		t.Fatalf("disabled readiness=%+v", body)
	}
	if strings.Contains(strings.ToLower(response.Body.String()), "secret") {
		t.Fatalf("readiness exposed sensitive configuration: %s", response.Body)
	}
}

func TestPaymentReadinessReportsPaystackCallbackAndWebhookEndpoint(t *testing.T) {
	h, err := NewReadinessHTTP(&memoryGateway{available: true}, "https://hotspot.example.test/portal.html")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	h.get(response, httptest.NewRequest(http.MethodGet, "/api/v1/payments/readiness", nil))

	var body readinessResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Provider != "paystack" || body.CheckoutStatus != "READY" || body.CallbackURL != "https://hotspot.example.test/portal.html" || body.WebhookURL != "https://hotspot.example.test/webhooks/paystack" {
		t.Fatalf("Paystack readiness=%+v", body)
	}
}
