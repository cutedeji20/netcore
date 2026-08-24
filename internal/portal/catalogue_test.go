package portal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type memoryCatalogueStore struct {
	tenantID     string
	found        bool
	resolvedSlug string
	plans        []PublicPlan
	err          error
}

func (s *memoryCatalogueStore) ResolveTenant(_ context.Context, slug string) (string, bool, error) {
	s.resolvedSlug = slug
	return s.tenantID, s.found, s.err
}

func (s *memoryCatalogueStore) ListPublishedPlans(_ context.Context, tenantID string) ([]PublicPlan, error) {
	if tenantID != s.tenantID {
		return nil, errors.New("unexpected tenant")
	}
	return s.plans, s.err
}

func TestCatalogueUsesConfiguredTenantAndReturnsPublicFieldsOnly(t *testing.T) {
	store := &memoryCatalogueStore{
		tenantID: portalTestTenantID,
		found:    true,
		plans: []PublicPlan{{
			ID: "33333333-3333-4333-8333-333333333333", Name: "Day pass", Description: "Fast access",
			PriceMinor: 50000, Currency: "NGN", DurationSeconds: 86400,
			DownloadBPS: 20_000_000, UploadBPS: 10_000_000, MaxDevices: 2, MaxConcurrentSessions: 1,
		}},
	}
	service, err := NewCatalogueService(store, "data-hub")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewCatalogueHTTP(service)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	handler.Routes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/portal/catalogue?tenant=other-tenant", nil))

	if response.Code != http.StatusOK || store.resolvedSlug != "data-hub" {
		t.Fatalf("status=%d resolved_tenant=%q body=%s", response.Code, store.resolvedSlug, response.Body)
	}
	for _, forbidden := range []string{"tenant_id", "status", "created_at", "updated_at", "quota", "active_subscriptions"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("public catalogue leaked %q: %s", forbidden, response.Body)
		}
	}
	for _, required := range []string{"Day pass", "price_minor", "duration_seconds", "download_bps", "max_devices"} {
		if !strings.Contains(response.Body.String(), required) {
			t.Fatalf("public catalogue omitted %q: %s", required, response.Body)
		}
	}
}

func TestCatalogueReturnsNotFoundForAnUnresolvedConfiguredTenant(t *testing.T) {
	service, err := NewCatalogueService(&memoryCatalogueStore{}, "data-hub")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewCatalogueHTTP(service)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	handler.Routes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/portal/catalogue", nil))
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "PORTAL_UNAVAILABLE") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}
