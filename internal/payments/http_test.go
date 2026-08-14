package payments

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/netcore-isp/netcore/internal/auth"
)

func TestInitiateRejectsBrowserAmountAndRequiresIdempotencyKey(t *testing.T) {
	store := &memoryPaymentStore{}
	h, err := NewHTTP(newPaymentService(t, store, &memoryGateway{available: true}), []string{"https://portal.example.test"})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(`{"plan_id":"`+paymentTestPlan+`","amount_minor":1}`))
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: paymentTestTenant, UserID: paymentTestUser}))
	response := httptest.NewRecorder()
	h.initiate(response, request)
	if response.Code != http.StatusBadRequest || store.prepareCalls != 0 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(`{"plan_id":"`+paymentTestPlan+`"}`))
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: paymentTestTenant, UserID: paymentTestUser}))
	response = httptest.NewRecorder()
	h.initiate(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "IDEMPOTENCY_KEY_REQUIRED") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestInitiateRejectsCrossSiteOrigin(t *testing.T) {
	h, err := NewHTTP(newPaymentService(t, &memoryPaymentStore{}, &memoryGateway{available: true}), []string{"https://portal.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/payments", strings.NewReader(`{"plan_id":"`+paymentTestPlan+`"}`))
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("Idempotency-Key", "payment-retry-key-0001")
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: paymentTestTenant, UserID: paymentTestUser}))
	response := httptest.NewRecorder()
	h.initiate(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "CSRF_REJECTED") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}
