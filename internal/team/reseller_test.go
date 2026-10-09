package team

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/netcore-isp/netcore/internal/auth"
)

type resellerStoreStub struct {
	called       bool
	tenant, user string
	enabled      bool
	actor        MutationActor
	err          error
}

func (s *resellerStoreStub) SetReseller(_ context.Context, tenantID, userID string, enabled bool, actor MutationActor) error {
	s.called, s.tenant, s.user, s.enabled, s.actor = true, tenantID, userID, enabled, actor
	return s.err
}
func (s *resellerStoreStub) IsReseller(context.Context, string, string) (bool, error) {
	return s.enabled, nil
}

func TestResellerDesignationRequiresStepUp(t *testing.T) {
	store := &resellerStoreStub{}
	service, err := NewResellerService(store, rejectingResellerStepUp{})
	if err != nil {
		t.Fatal(err)
	}
	principal := auth.Principal{TenantID: teamTestTenantID, UserID: "22222222-2222-4222-8222-222222222222", Permissions: map[string]struct{}{"team.write": {}}}
	err = service.SetReseller(context.Background(), principal, "33333333-3333-4333-8333-333333333333", true, "password", "123456")
	if !errors.Is(err, ErrStepUpFailed) || store.called {
		t.Fatalf("err=%v called=%v", err, store.called)
	}
}

func TestResellerDesignationPassesActorAndState(t *testing.T) {
	store := &resellerStoreStub{}
	service, err := NewResellerService(store, acceptingStepUp{})
	if err != nil {
		t.Fatal(err)
	}
	principal := auth.Principal{TenantID: teamTestTenantID, UserID: "22222222-2222-4222-8222-222222222222", Permissions: map[string]struct{}{"team.write": {}}}
	target := "33333333-3333-4333-8333-333333333333"
	if err := service.SetReseller(context.Background(), principal, target, true, "password", "123456"); err != nil {
		t.Fatal(err)
	}
	if !store.called || store.tenant != teamTestTenantID || store.user != target || !store.enabled || store.actor.UserID != principal.UserID {
		t.Fatalf("store=%+v", store)
	}
}

func TestResellerHTTPRequiresAuthenticatedStepUp(t *testing.T) {
	store := &resellerStoreStub{}
	service, err := NewResellerService(store, acceptingStepUp{})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := NewHTTP(&memoryStore{}, 25, 100)
	if err := h.ConfigureResellers(service); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/users/33333333-3333-4333-8333-333333333333/reseller", strings.NewReader(`{"enabled":true,"password":"secret","mfa_code":"123456"}`))
	request.SetPathValue("id", "33333333-3333-4333-8333-333333333333")
	response := httptest.NewRecorder()
	h.setReseller(response, request)
	if response.Code != http.StatusUnauthorized || store.called {
		t.Fatalf("status=%d called=%v", response.Code, store.called)
	}
	principal := auth.Principal{TenantID: teamTestTenantID, UserID: "22222222-2222-4222-8222-222222222222", Permissions: map[string]struct{}{"team.write": {}}}
	request = httptest.NewRequest(http.MethodPut, "/api/v1/users/33333333-3333-4333-8333-333333333333/reseller", strings.NewReader(`{"enabled":true,"password":"secret","mfa_code":"123456"}`))
	request.SetPathValue("id", "33333333-3333-4333-8333-333333333333")
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), principal))
	response = httptest.NewRecorder()
	h.setReseller(response, request)
	if response.Code != http.StatusNoContent || !store.called || !store.enabled {
		t.Fatalf("status=%d store=%+v body=%s", response.Code, store, response.Body)
	}
}

type rejectingResellerStepUp struct{}

func (rejectingResellerStepUp) VerifyStepUp(context.Context, auth.StepUpInput) error {
	return errors.New("denied")
}
