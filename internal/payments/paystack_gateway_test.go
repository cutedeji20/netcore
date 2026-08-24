package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type paymentSecrets map[string]string

func (s paymentSecrets) Resolve(_ context.Context, reference string) (string, error) {
	return s[reference], nil
}

func newPaystackTestGateway(t *testing.T, handler http.HandlerFunc) (*PaystackGateway, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	ref := "payments.paystack.secret_key"
	gateway, err := NewPaystackGateway(paymentSecrets{ref: "sk_test_not_a_real_secret"}, ref, &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	gateway.baseURL = server.URL
	return gateway, ref
}

func TestPaystackInitializeUsesFrozenMinorUnitsAndServerReference(t *testing.T) {
	reference := "pay-1234567890abcdef1234567890abcdef"
	gateway, _ := newPaystackTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/transaction/initialize" {
			t.Fatalf("request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk_test_not_a_real_secret" {
			t.Fatal("missing server authorization")
		}
		var request map[string]string
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["amount"] != "500000" || request["currency"] != "NGN" || request["reference"] != reference || request["email"] != "customer@example.test" || request["callback_url"] != "https://portal.example.test/portal.html" {
			t.Fatalf("request body = %#v", request)
		}
		_, _ = w.Write([]byte(`{"status":true,"data":{"authorization_url":"https://checkout.paystack.test/abc","reference":"` + reference + `","new_field":"ignored"}}`))
	})

	checkout, err := gateway.Initialize(context.Background(), GatewayInitialization{Reference: reference, AmountMinor: 500000, Currency: "NGN", CustomerEmail: "customer@example.test", CallbackURL: "https://portal.example.test/portal.html"})
	if err != nil || checkout.AuthorizationURL != "https://checkout.paystack.test/abc" {
		t.Fatalf("checkout=%+v err=%v", checkout, err)
	}
}

func TestPaystackVerifyReturnsOnlyProviderFacts(t *testing.T) {
	reference := "pay-1234567890abcdef1234567890abcdef"
	gateway, _ := newPaystackTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/transaction/verify/"+reference {
			t.Fatalf("request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":true,"data":{"reference":"` + reference + `","status":"success","amount":500000,"currency":"NGN","paid_at":"2026-08-13T12:00:00Z","ip_address":"not-retained"}}`))
	})

	verification, err := gateway.Verify(context.Background(), reference)
	if err != nil || verification.Status != StatusSuccess || verification.AmountMinor != 500000 || verification.Currency != "NGN" || verification.VerifiedAt.IsZero() {
		t.Fatalf("verification=%+v err=%v", verification, err)
	}
}

func TestPaystackWebhookUsesRawHMACAndMinimalIdentity(t *testing.T) {
	secret := "sk_test_not_a_real_secret"
	raw := []byte(`{"event":"charge.success","data":{"id":123456789,"reference":"pay-1234567890abcdef1234567890abcdef","amount":500000}}`)
	mac := hmac.New(sha512.New, []byte(secret))
	_, _ = mac.Write(raw)
	signature := hex.EncodeToString(mac.Sum(nil))

	gateway, err := NewPaystackGateway(paymentSecrets{"payments.paystack.secret_key": secret}, "payments.paystack.secret_key", &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := gateway.VerifyWebhookSignature(context.Background(), raw, signature); err != nil {
		t.Fatal(err)
	}
	if err := gateway.VerifyWebhookSignature(context.Background(), append(raw, ' '), signature); err == nil {
		t.Fatal("signature accepted a reserialized payload")
	}
	event, err := gateway.ParseWebhook(raw)
	if err != nil || event.EventID != "charge.success:123456789" || event.Reference != "pay-1234567890abcdef1234567890abcdef" {
		t.Fatalf("event=%+v err=%v", event, err)
	}
	if _, err := gateway.ParseWebhook([]byte(`{"event":"charge.success","data":{"id":1,"reference":"other"}}`)); err == nil {
		t.Fatal("invalid provider reference was accepted")
	}
	if strings.Contains(signature, secret) {
		t.Fatal("test signature unexpectedly contains its secret")
	}
}
