package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/integrations"
)

type testTenantSquadResolver struct{}

func (testTenantSquadResolver) Resolve(_ context.Context, tenantID string, provider integrations.Provider) ([]byte, integrations.CredentialMetadata, error) {
	if tenantID != "tenant-data-hub" || provider != integrations.ProviderSquad {
		return nil, integrations.CredentialMetadata{}, ErrGatewayUnavailable
	}
	return []byte("sandbox_sk_dashboard_key"), integrations.CredentialMetadata{SquadMode: "TEST"}, nil
}

func TestTenantSquadInitializeUsesHostedCheckout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/transaction/initiate" || r.Header.Get("Authorization") != "Bearer sandbox_sk_dashboard_key" {
			t.Fatalf("request=%s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		var body struct {
			Amount       string            `json:"amount"`
			Currency     string            `json:"currency"`
			InitiateType string            `json:"initiate_type"`
			Reference    string            `json:"transaction_ref"`
			Metadata     map[string]string `json:"metadata"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Amount != "500000" || body.Currency != "NGN" || body.InitiateType != "inline" || body.Reference != "pay-0123456789abcdef0123456789abcdef" || body.Metadata["netcore_payment_reference"] != body.Reference {
			t.Fatalf("request body=%#v", body)
		}
		_, _ = w.Write([]byte(`{"status":200,"data":{"transaction_ref":"pay-0123456789abcdef0123456789abcdef","checkout_url":"https://sandbox-pay.squadco.com/checkout"}}`))
	}))
	defer server.Close()
	gateway, err := NewTenantSquadGateway(testTenantSquadResolver{}, "tenant-data-hub", &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	gateway.baseURL = server.URL
	checkout, err := gateway.Initialize(context.Background(), GatewayInitialization{Reference: "pay-0123456789abcdef0123456789abcdef", AmountMinor: 500000, Currency: "NGN", CustomerEmail: "customer@example.test", CallbackURL: "https://portal.example.test/portal.html"})
	if err != nil || checkout.AuthorizationURL != "https://sandbox-pay.squadco.com/checkout" {
		t.Fatalf("checkout=%#v err=%v", checkout, err)
	}
}

func TestTenantSquadVerifyMatchesTransaction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/transaction" || r.URL.Query().Get("reference") != "pay-0123456789abcdef0123456789abcdef" {
			t.Fatalf("request=%s %s query=%v", r.Method, r.URL.Path, r.URL.Query())
		}
		_, _ = w.Write([]byte(`{"status":200,"success":true,"data":[{"transaction_ref":"pay-0123456789abcdef0123456789abcdef","transaction_status":"success","transaction_amount":500000,"transaction_currency_id":"NGN","created_at":"2026-09-11T10:15:00.000+00:00"}]}`))
	}))
	defer server.Close()
	gateway, err := NewTenantSquadGateway(testTenantSquadResolver{}, "tenant-data-hub", &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	gateway.baseURL = server.URL
	verification, err := gateway.Verify(context.Background(), "pay-0123456789abcdef0123456789abcdef")
	if err != nil || verification.Status != StatusSuccess || verification.AmountMinor != 500000 || verification.Currency != "NGN" || verification.VerifiedAt.IsZero() {
		t.Fatalf("verification=%#v err=%v", verification, err)
	}
}

func TestParseSquadWebhookReturnsMinimalIdentity(t *testing.T) {
	raw := []byte(`{"Event":"charge_successful","TransactionRef":"pay-0123456789abcdef0123456789abcdef","Body":{"transaction_ref":"pay-0123456789abcdef0123456789abcdef","gateway_ref":"gw-123"}}`)
	event, err := parseSquadWebhook(raw)
	if err != nil {
		t.Fatalf("parseSquadWebhook: %v", err)
	}
	if event.Provider != squadName || event.EventType != squadChargeSuccess || event.Reference != "pay-0123456789abcdef0123456789abcdef" || event.EventID != "charge_successful:gw-123" {
		t.Fatalf("event=%#v", event)
	}
}

func TestTenantSquadWebhookUsesDocumentedHMACHeader(t *testing.T) {
	secret := []byte("sandbox_sk_dashboard_key")
	gateway, err := NewTenantSquadGateway(testTenantSquadResolver{}, "tenant-data-hub", &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"Event":"charge_successful","TransactionRef":"pay-0123456789abcdef0123456789abcdef","Body":{"transaction_ref":"pay-0123456789abcdef0123456789abcdef","gateway_ref":"gw-123"}}`)
	mac := hmac.New(sha512.New, secret)
	_, _ = mac.Write(raw)
	if err := gateway.VerifyWebhookSignature(context.Background(), raw, hex.EncodeToString(mac.Sum(nil))); err != nil {
		t.Fatalf("VerifyWebhookSignature: %v", err)
	}
}
