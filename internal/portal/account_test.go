package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
)

type memoryAccountStore struct {
	tenantID string
	userID   string
	account  CustomerAccount
	found    bool
	err      error
}

func (s *memoryAccountStore) CustomerAccount(_ context.Context, tenantID, userID string) (CustomerAccount, bool, error) {
	s.tenantID = tenantID
	s.userID = userID
	return s.account, s.found, s.err
}

func TestCustomerAccountUsesOnlyTheAuthenticatedPrincipalScope(t *testing.T) {
	expiresAt := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	store := &memoryAccountStore{
		found: true,
		account: CustomerAccount{
			Subscriptions: []CustomerSubscription{{
				PlanName: "Weekly access", Status: "ACTIVE", PaymentStatus: "PAID", ExpiresAt: &expiresAt,
			}},
			Payments: []CustomerPayment{{
				Reference: "pay-0123456789abcdef0123456789abcdef", AmountMinor: 250000, Currency: "NGN", Status: "SUCCESS", CreatedAt: expiresAt,
			}},
		},
	}
	service, err := NewAccountService(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewAccountHTTP(service)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/portal/account?tenant=attacker&user=other", nil)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{
		TenantID: portalTestTenantID,
		UserID:   portalTestUserID,
	}))
	response := httptest.NewRecorder()

	handler.account(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if store.tenantID != portalTestTenantID || store.userID != portalTestUserID {
		t.Fatalf("account scope tenant=%q user=%q", store.tenantID, store.userID)
	}
	var body customerAccountResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Subscriptions) != 1 || body.Data.Subscriptions[0].PlanName != "Weekly access" || body.Data.Subscriptions[0].ExpiresAt == nil || !body.Data.Subscriptions[0].ExpiresAt.Equal(expiresAt) {
		t.Fatalf("subscriptions=%+v", body.Data.Subscriptions)
	}
	if len(body.Data.Payments) != 1 || body.Data.Payments[0].Reference != "pay-0123456789abcdef0123456789abcdef" || body.Data.Payments[0].AmountMinor != 250000 {
		t.Fatalf("payments=%+v", body.Data.Payments)
	}
	for _, forbidden := range []string{"tenant_id", "user_id", "customer_id", "gateway", "subscription_id"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("customer account leaked %q: %s", forbidden, response.Body)
		}
	}
}

func TestCustomerAccountRejectsRequestsWithoutAnAuthenticatedPrincipal(t *testing.T) {
	service, err := NewAccountService(&memoryAccountStore{found: true})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewAccountHTTP(service)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()

	handler.account(response, httptest.NewRequest(http.MethodGet, "/api/v1/portal/account", nil))

	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "UNAUTHENTICATED") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestCustomerAccountDoesNotExposeAStaffSessionWithoutACustomerProfile(t *testing.T) {
	service, err := NewAccountService(&memoryAccountStore{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewAccountHTTP(service)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/portal/account", nil)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: portalTestTenantID, UserID: portalTestUserID}))
	response := httptest.NewRecorder()

	handler.account(response, request)

	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "CUSTOMER_ACCOUNT_NOT_FOUND") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}
