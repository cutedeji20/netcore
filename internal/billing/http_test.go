package billing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
)

const billingTestTenantID = "11111111-1111-4111-8111-111111111111"

type memoryStore struct {
	tenantID     string
	options      ListOptions
	page         Page
	err          error
	clearRequest ClearRequest
	clearActor   MutationActor
	clearCount   int
	metrics      Metrics
}

func (s *memoryStore) Metrics(_ context.Context, tenantID string) (Metrics, error) {
	s.tenantID = tenantID
	return s.metrics, s.err
}

func TestMetricsUsesTenantScopedAggregate(t *testing.T) {
	handler, store := newTestHTTP(t)
	store.metrics = Metrics{CollectedThisMonthMinor: 51500, OpenInvoiceMinor: 50000, SuccessfulPayments: 1, FinishedPayments: 2, NeedsReview: 1}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/billing/metrics", nil)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: billingTestTenantID}))
	response := httptest.NewRecorder()
	handler.metrics(response, request)
	if response.Code != http.StatusOK || store.tenantID != billingTestTenantID || !strings.Contains(response.Body.String(), `"collected_this_month_minor":51500`) {
		t.Fatalf("metrics status=%d tenant=%q body=%s", response.Code, store.tenantID, response.Body.String())
	}
}

func (s *memoryStore) List(_ context.Context, tenantID string, options ListOptions) (Page, error) {
	s.tenantID = tenantID
	s.options = options
	return s.page, s.err
}

func (s *memoryStore) Clear(_ context.Context, tenantID string, actor MutationActor, request ClearRequest) (int, error) {
	s.tenantID, s.clearActor, s.clearRequest = tenantID, actor, request
	return s.clearCount, s.err
}

type stepUpStub struct{ err error }

func (s stepUpStub) VerifyStepUp(context.Context, auth.StepUpInput) error { return s.err }

func newTestHTTP(t *testing.T) (*HTTP, *memoryStore) {
	t.Helper()
	verifiedAt := time.Date(2026, 8, 12, 13, 18, 0, 0, time.UTC)
	store := &memoryStore{
		page: Page{
			Transactions: []Transaction{{
				ID:                "22222222-2222-4222-8222-222222222222",
				Source:            SourcePayment,
				Reference:         "PAY-749202",
				CustomerID:        "33333333-3333-4333-8333-333333333333",
				CustomerNumber:    "CUS-10482",
				CustomerFirstName: "Chika",
				CustomerLastName:  "Nwosu",
				AmountMinor:       8_500_000,
				Currency:          "NGN",
				Status:            "SUCCESS",
				RecordedAt:        verifiedAt,
				VerifiedAt:        &verifiedAt,
			}},
		},
	}
	handler, err := NewHTTP(store, 25, 100)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store
}

func TestListUsesTenantScopedCursorOptions(t *testing.T) {
	handler, store := newTestHTTP(t)
	cursor := encodeCursor(Cursor{
		RecordedAt: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC),
		Source:     SourceInvoice,
		ID:         "44444444-4444-4444-8444-444444444444",
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/billing/transactions?limit=10&q=chika&source=PAYMENT&cursor="+cursor, nil)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: billingTestTenantID}))
	response := httptest.NewRecorder()

	handler.list(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	if store.tenantID != billingTestTenantID || store.options.Limit != 10 || store.options.Search != "chika" || store.options.Source != SourcePayment || store.options.Cursor.ID != "44444444-4444-4444-8444-444444444444" {
		t.Fatalf("unexpected store request: tenant=%q options=%+v", store.tenantID, store.options)
	}
	var body listResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].AmountMinor != "8500000" || body.Data[0].Reference != "PAY-749202" || body.Data[0].VerifiedAt == nil {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestListRejectsUnsafePageParameters(t *testing.T) {
	handler, _ := newTestHTTP(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/billing/transactions?source=REFUND", nil)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: billingTestTenantID}))
	response := httptest.NewRecorder()

	handler.list(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
}

func TestListRejectsMissingPrincipal(t *testing.T) {
	handler, _ := newTestHTTP(t)
	response := httptest.NewRecorder()

	handler.list(response, httptest.NewRequest(http.MethodGet, "/api/v1/billing/transactions", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
}

func TestClearAttemptsRequiresStepUpAndRestrictsScope(t *testing.T) {
	handler, store := newTestHTTP(t)
	service, err := NewService(store, stepUpStub{})
	if err != nil {
		t.Fatal(err)
	}
	handler.ConfigureLifecycle(service)
	principal := auth.Principal{TenantID: billingTestTenantID, UserID: "44444444-4444-4444-8444-444444444444", Permissions: map[string]struct{}{"billing.write": {}}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/billing/payment-attempts/clear", strings.NewReader(`{"scope":"PENDING","password":"current","mfa_code":"123456"}`))
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), principal))
	response := httptest.NewRecorder()
	handler.clearAttempts(response, request)
	if response.Code != http.StatusOK || store.clearRequest.Scope != ClearPending || store.clearActor.UserID != principal.UserID {
		t.Fatalf("status=%d request=%+v actor=%+v", response.Code, store.clearRequest, store.clearActor)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/billing/payment-attempts/clear", strings.NewReader(`{"scope":"SELECTED","payment_ids":["22222222-2222-4222-8222-222222222222"],"password":"current","mfa_code":"123456"}`))
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), principal))
	response = httptest.NewRecorder()
	handler.clearAttempts(response, request)
	if response.Code != http.StatusOK || len(store.clearRequest.PaymentIDs) != 1 {
		t.Fatalf("selected status=%d request=%+v", response.Code, store.clearRequest)
	}
}

func TestClearAttemptsRejectsInvalidScopeAndBadStepUp(t *testing.T) {
	handler, store := newTestHTTP(t)
	service, _ := NewService(store, stepUpStub{err: errors.New("bad")})
	handler.ConfigureLifecycle(service)
	principal := auth.Principal{TenantID: billingTestTenantID, UserID: "44444444-4444-4444-8444-444444444444", Permissions: map[string]struct{}{"billing.write": {}}}
	for _, body := range []string{`{"scope":"SUCCESS","password":"current","mfa_code":"123456"}`, `{"scope":"PENDING","password":"current","mfa_code":"123456"}`} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/billing/payment-attempts/clear", strings.NewReader(body))
		request = request.WithContext(auth.ContextWithPrincipal(request.Context(), principal))
		response := httptest.NewRecorder()
		handler.clearAttempts(response, request)
		if response.Code != http.StatusBadRequest && response.Code != http.StatusUnauthorized {
			t.Fatalf("body=%s status=%d", body, response.Code)
		}
	}
}

func TestCursorRoundTripAndRejectsExtraFields(t *testing.T) {
	cursor := Cursor{
		RecordedAt: time.Date(2026, 8, 12, 10, 0, 0, 123, time.UTC),
		Source:     SourcePayment,
		ID:         "55555555-5555-4555-8555-555555555555",
	}
	decoded, err := decodeCursor(encodeCursor(cursor))
	if err != nil || decoded != cursor {
		t.Fatalf("cursor = %+v, %v", decoded, err)
	}
	extra := base64.RawURLEncoding.EncodeToString([]byte(`{"recorded_at":"2026-08-12T10:00:00Z","source":"PAYMENT","id":"55555555-5555-4555-8555-555555555555","extra":true}`))
	if _, err := decodeCursor(extra); err == nil {
		t.Fatal("cursor with an unexpected field was accepted")
	}
}
