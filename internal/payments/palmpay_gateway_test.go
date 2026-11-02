package payments

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/integrations"
)

type palmPayResolverStub struct {
	credential []byte
	mode       string
}

func (r palmPayResolverStub) Resolve(context.Context, string, integrations.Provider) ([]byte, integrations.CredentialMetadata, error) {
	return append([]byte(nil), r.credential...), integrations.CredentialMetadata{PalmPayMode: r.mode}, nil
}

func newPalmPayTestCredential(t *testing.T) ([]byte, *rsa.PrivateKey) {
	t.Helper()
	merchantKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	palmpayKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	merchantDER, err := x509.MarshalPKCS8PrivateKey(merchantKey)
	if err != nil {
		t.Fatal(err)
	}
	palmpayDER, err := x509.MarshalPKIXPublicKey(&palmpayKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := json.Marshal(PalmPayCredential{
		Authorization: "merchant-auth", MerchantPrivateKey: base64.StdEncoding.EncodeToString(merchantDER),
		PalmPayPublicKey: base64.StdEncoding.EncodeToString(palmpayDER), CountryCode: "NG",
	})
	if err != nil {
		t.Fatal(err)
	}
	return credential, palmpayKey
}

func TestPalmPayInitializeAndVerifyUseFrozenFacts(t *testing.T) {
	credential, _ := newPalmPayTestCredential(t)
	reference := "pay-0123456789abcdef0123456789ab"
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer merchant-auth" || r.Header.Get("CountryCode") != "NG" || r.Header.Get("Signature") == "" {
			t.Fatalf("unexpected PalmPay request: %s %s headers=%v", r.Method, r.URL.Path, r.Header)
		}
		var body map[string]any
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/payment/merchant/createorder":
			if body["orderId"] != reference || body["amount"].(json.Number).String() != "9100" || body["notifyUrl"] != "https://portal.example.test/webhooks/palmpay" {
				t.Fatalf("unexpected initialization payload: %#v", body)
			}
			_, _ = w.Write([]byte(`{"respCode":"00000000","data":{"orderNo":"pp-1","orderStatus":1,"checkoutUrl":"https://checkout.palmpay.test/order","orderAmount":9100,"currency":"NGN"}}`))
		case "/api/v2/payment/merchant/order/queryStatus":
			if body["orderId"] != reference {
				t.Fatalf("unexpected verification payload: %#v", body)
			}
			_, _ = w.Write([]byte(`{"respCode":"00000000","data":{"orderId":"` + reference + `","orderNo":"pp-1","currency":"NGN","amount":9100,"orderStatus":2,"completedTime":1793520000000}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	gateway, err := NewTenantPalmPayGateway(palmPayResolverStub{credential: credential, mode: "TEST"}, "tenant", &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	gateway.baseURL = server.URL
	gateway.now = func() time.Time { return time.Unix(1793519000, 0).UTC() }
	checkout, err := gateway.Initialize(context.Background(), GatewayInitialization{
		Reference: reference, AmountMinor: 9100, Currency: "NGN", CustomerEmail: "buyer@example.test", CallbackURL: "https://portal.example.test/portal.html",
	})
	if err != nil || checkout.AuthorizationURL != "https://checkout.palmpay.test/order" {
		t.Fatalf("checkout=%#v err=%v", checkout, err)
	}
	verification, err := gateway.Verify(context.Background(), reference)
	if err != nil || verification.Status != StatusSuccess || verification.AmountMinor != 9100 || verification.Currency != "NGN" || verification.VerifiedAt.IsZero() {
		t.Fatalf("verification=%#v err=%v", verification, err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d, want 2", calls)
	}
}

func TestPalmPayWebhookRequiresFullBodySignature(t *testing.T) {
	credential, palmpayKey := newPalmPayTestCredential(t)
	gateway, err := NewTenantPalmPayGateway(palmPayResolverStub{credential: credential, mode: "LIVE"}, "tenant", &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"orderId": "pay-0123456789abcdef0123456789ab", "orderNo": "24240001", "amount": int64(9100),
		"currency": "NGN", "orderStatus": 2, "completeTime": int64(1793520000000),
	}
	canonical, err := palmPayCanonical(payload)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := rsa.SignPKCS1v15(rand.Reader, palmpayKey, crypto.SHA1, palmPaySigningDigest(canonical))
	if err != nil {
		t.Fatal(err)
	}
	payload["sign"] = base64.StdEncoding.EncodeToString(signature)
	raw, _ := json.Marshal(payload)
	if err := gateway.VerifyWebhookSignature(context.Background(), raw, ""); err != nil {
		t.Fatalf("signature rejected: %v", err)
	}
	event, err := gateway.ParseWebhook(raw)
	if err != nil || event.Provider != palmpayName || event.EventType != palmpayChargeSuccess || !strings.Contains(event.EventID, "24240001") {
		t.Fatalf("event=%#v err=%v", event, err)
	}

	payload["amount"] = int64(9200)
	tampered, _ := json.Marshal(payload)
	if err := gateway.VerifyWebhookSignature(context.Background(), tampered, ""); err == nil {
		t.Fatal("tampered webhook signature accepted")
	}
}

func TestNewReferenceFitsPalmPayAndLegacyReferenceStillValid(t *testing.T) {
	reference, err := newReference()
	if err != nil {
		t.Fatal(err)
	}
	if len(reference) != 32 || !validPalmPayReference(reference) {
		t.Fatalf("reference=%q does not fit PalmPay", reference)
	}
	if !validReference("pay-0123456789abcdef0123456789abcdef") {
		t.Fatal("legacy 36-character reference was rejected")
	}
}
