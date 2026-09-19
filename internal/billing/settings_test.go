package billing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/netcore-isp/netcore/internal/auth"
)

type memorySettingsStore struct {
	settings Settings
	actor    MutationActor
	saved    bool
}

func (s *memorySettingsStore) LoadSettings(context.Context, string) (Settings, error) {
	return s.settings, nil
}
func (s *memorySettingsStore) SaveSettings(_ context.Context, _ string, actor MutationActor, settings Settings) error {
	s.settings, s.actor, s.saved = settings, actor, true
	return nil
}

func TestBankChargeSettingsParsesExactDecimalAndAuditsUpdate(t *testing.T) {
	store := &memorySettingsStore{settings: Settings{Currency: "NGN"}}
	service, err := NewSettingsService(store, stepUpStub{})
	if err != nil {
		t.Fatal(err)
	}
	httpHandler, err := NewSettingsHTTP(service)
	if err != nil {
		t.Fatal(err)
	}
	principal := auth.Principal{TenantID: billingTestTenantID, UserID: "44444444-4444-4444-8444-444444444444", Permissions: map[string]struct{}{"billing.write": {}}}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/billing/settings", strings.NewReader(`{"fixed_bank_charge":"15.00","password":"current","mfa_code":"123456"}`))
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), principal))
	response := httptest.NewRecorder()
	httpHandler.put(response, request)
	if response.Code != http.StatusOK || !store.saved || store.settings.FixedBankChargeMinor != 1500 || store.settings.Currency != "NGN" || store.actor.UserID != principal.UserID {
		t.Fatalf("status=%d saved=%t settings=%+v actor=%+v", response.Code, store.saved, store.settings, store.actor)
	}
}

func TestBankChargeSettingsRequiresBillingWrite(t *testing.T) {
	store := &memorySettingsStore{settings: Settings{Currency: "NGN"}}
	service, _ := NewSettingsService(store, stepUpStub{})
	httpHandler, _ := NewSettingsHTTP(service)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/billing/settings", strings.NewReader(`{"fixed_bank_charge":"15.00","password":"current","mfa_code":"123456"}`))
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: billingTestTenantID, UserID: "44444444-4444-4444-8444-444444444444"}))
	response := httptest.NewRecorder()
	httpHandler.put(response, request)
	if response.Code != http.StatusForbidden || store.saved {
		t.Fatalf("status=%d saved=%t", response.Code, store.saved)
	}
}
