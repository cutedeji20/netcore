package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/integrations"
	"github.com/netcore-isp/netcore/internal/payments"
)

type testTenantResendResolver struct {
	tenantID string
	provider integrations.Provider
}

func (r *testTenantResendResolver) Resolve(_ context.Context, tenantID string, provider integrations.Provider) ([]byte, integrations.CredentialMetadata, error) {
	r.tenantID = tenantID
	r.provider = provider
	return []byte("re_dashboard_managed_key"), integrations.CredentialMetadata{SenderEmail: "DataHub <hotspot@example.test>"}, nil
}

func TestTenantResendNotifierLoadsDashboardCredentialForItsTenant(t *testing.T) {
	// This fails if public account email falls back to a deployment key or can
	// send with a credential selected outside the portal tenant.
	var request struct {
		From string `json:"from"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer re_dashboard_managed_key" {
			t.Fatalf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	resolver := &testTenantResendResolver{}
	client := server.Client()
	client.Timeout = time.Second
	notifier, err := NewTenantResendNotifier(resolver, "tenant-data-hub", client)
	if err != nil {
		t.Fatal(err)
	}
	notifier.baseURL = server.URL
	if err := notifier.SendOTP(context.Background(), auth.OTPEmailVerification, "customer@example.com", "482913", time.Now().Add(10*time.Minute)); err != nil {
		t.Fatalf("SendOTP: %v", err)
	}
	if resolver.tenantID != "tenant-data-hub" || resolver.provider != integrations.ProviderResend {
		t.Fatalf("resolver scope = %q %q", resolver.tenantID, resolver.provider)
	}
	if request.From != "DataHub <hotspot@example.test>" {
		t.Fatalf("sender = %q", request.From)
	}
}

func TestTenantResendNotifierLoadsDashboardCredentialForPaymentReceipt(t *testing.T) {
	// This fails if the receipt worker sends with a deployment secret instead
	// of the active tenant's dashboard-managed Resend key and sender.
	var request struct {
		From string `json:"from"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer re_dashboard_managed_key" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "payment.receipt.requested/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
			t.Fatalf("idempotency key = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := server.Client()
	client.Timeout = time.Second
	resolver := &testTenantResendResolver{}
	notifier, err := NewTenantResendNotifier(resolver, "tenant-data-hub", client)
	if err != nil {
		t.Fatal(err)
	}
	notifier.baseURL = server.URL
	receipt := payments.ReceiptEmail{
		EventID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", To: "customer@example.com", PlanName: "Weekly access",
		Reference: "pay_12345678901234567890123456789012", AmountMinor: 250000, Currency: "NGN",
		StartsAt: time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC),
	}
	if err := notifier.SendPaymentReceipt(context.Background(), receipt); err != nil {
		t.Fatalf("SendPaymentReceipt: %v", err)
	}
	if resolver.tenantID != "tenant-data-hub" || resolver.provider != integrations.ProviderResend || request.From != "DataHub <hotspot@example.test>" {
		t.Fatalf("receipt resolver/sender = %q %q %q", resolver.tenantID, resolver.provider, request.From)
	}
}
