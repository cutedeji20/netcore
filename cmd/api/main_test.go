package main

import (
	"context"
	"testing"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/config"
	"github.com/netcore-isp/netcore/internal/integrations"
)

type dashboardPaymentAccountStore struct{}

func (dashboardPaymentAccountStore) ResolveTenant(context.Context, string) (string, bool, error) {
	return "tenant-data-hub", true, nil
}

func (dashboardPaymentAccountStore) RegistrationPolicy(context.Context, string) (auth.RegistrationPolicy, error) {
	return auth.RegistrationPolicy{}, nil
}

func (dashboardPaymentAccountStore) PrepareEmailRegistration(context.Context, string, string, string, string) error {
	return nil
}

func (dashboardPaymentAccountStore) VerifyEmailAndEnsureCustomer(context.Context, string, string) error {
	return nil
}

func (dashboardPaymentAccountStore) ResetVerifiedPassword(context.Context, string, string, string) error {
	return nil
}

type dashboardPaymentCredentialStore struct{}

func (dashboardPaymentCredentialStore) LoadActive(context.Context, string, integrations.Provider) (integrations.Record, bool, error) {
	return integrations.Record{}, false, nil
}

func TestConfiguredPaymentGatewayUsesDashboardProviderWhenGatewayIsDisabled(t *testing.T) {
	resolver, err := integrations.NewCredentialResolver(dashboardPaymentCredentialStore{}, integrations.NewUnavailableKeyWrapper())
	if err != nil {
		t.Fatal(err)
	}

	gateway, webhook, err := configuredPaymentGateway(context.Background(), &config.Config{
		Portal:   config.Portal{TenantSlug: "data-hub"},
		Payments: config.Payments{Gateway: "disabled"},
	}, dashboardPaymentAccountStore{}, resolver)
	if err != nil {
		t.Fatalf("configuredPaymentGateway: %v", err)
	}
	if gateway == nil || gateway.Name() != "squad" {
		t.Fatalf("gateway = %#v, want dashboard-managed squad gateway", gateway)
	}
	if webhook == nil {
		t.Fatal("configuredPaymentGateway returned a nil webhook gateway")
	}
}

func TestHealthURL(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want string
	}{
		{name: "wildcard host", addr: ":8080", want: "http://127.0.0.1:8080/health/live"},
		{name: "explicit interface", addr: "127.0.0.1:9000", want: "http://127.0.0.1:9000/health/live"},
		{name: "all IPv4 interfaces", addr: "0.0.0.0:8080", want: "http://127.0.0.1:8080/health/live"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := healthURL(tt.addr)
			if err != nil {
				t.Fatalf("healthURL(%q): %v", tt.addr, err)
			}
			if got != tt.want {
				t.Fatalf("healthURL(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}

func TestHealthURLRejectsAddressWithoutPort(t *testing.T) {
	if _, err := healthURL("localhost"); err == nil {
		t.Fatal("healthURL accepted an address without a port")
	}
}

func TestDisabledKeyWrapperFailsClosed(t *testing.T) {
	// This fails if a deployment without its Key Vault KEK can enroll dynamic
	// MFA or integration secrets through a local fallback.
	wrapper, err := configuredIntegrationKeyWrapper(&config.Config{IntegrationCrypto: config.IntegrationCrypto{Backend: "disabled"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrapper.Wrap(context.Background(), make([]byte, 32)); err == nil {
		t.Fatal("disabled key wrapper accepted a data encryption key")
	}
}
