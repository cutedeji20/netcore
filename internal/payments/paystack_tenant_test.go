package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/integrations"
)

type testTenantPaystackResolver struct {
	tenantID string
	provider integrations.Provider
}

func (r *testTenantPaystackResolver) Resolve(_ context.Context, tenantID string, provider integrations.Provider) ([]byte, integrations.CredentialMetadata, error) {
	r.tenantID = tenantID
	r.provider = provider
	return []byte("sk_test_dashboard_managed_key"), integrations.CredentialMetadata{PaystackMode: "TEST"}, nil
}

func TestTenantPaystackGatewayLoadsDashboardCredentialForItsTenant(t *testing.T) {
	// This fails if checkout initialization can use a deployment secret or a
	// Paystack credential from outside the tenant that owns the portal.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/transaction/initialize" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk_test_dashboard_managed_key" {
			t.Fatalf("authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": true,
			"data": map[string]string{
				"reference":         "pay-1234567890abcdef1234567890abcdef",
				"authorization_url": "https://checkout.paystack.example.test/abc",
			},
		})
	}))
	defer server.Close()

	client := server.Client()
	client.Timeout = time.Second
	resolver := &testTenantPaystackResolver{}
	gateway, err := NewTenantPaystackGateway(resolver, "tenant-data-hub", client)
	if err != nil {
		t.Fatal(err)
	}
	gateway.baseURL = server.URL
	checkout, err := gateway.Initialize(context.Background(), GatewayInitialization{
		Reference: "pay-1234567890abcdef1234567890abcdef", AmountMinor: 250000,
		Currency: "NGN", CustomerEmail: "customer@example.com", CallbackURL: "https://portal.example.test/portal.html",
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if checkout.AuthorizationURL != "https://checkout.paystack.example.test/abc" {
		t.Fatalf("checkout = %#v", checkout)
	}
	if resolver.tenantID != "tenant-data-hub" || resolver.provider != integrations.ProviderPaystack {
		t.Fatalf("resolver scope = %q %q", resolver.tenantID, resolver.provider)
	}
}
