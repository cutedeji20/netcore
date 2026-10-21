package vouchers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
)

type revealLimiterStub struct {
	allowed bool
	calls   int
}

func (l *revealLimiterStub) AllowSlidingWindow(_ context.Context, _ string, limit int64, _ time.Duration) (bool, error) {
	l.calls++
	if limit != 5 {
		return false, ErrCodeUnavailable
	}
	return l.allowed, nil
}

func TestRevealRateLimitAndNoStore(t *testing.T) {
	h, _ := newTestHTTP(t)
	store := &issuingStoreStub{}
	issuer, _ := NewIssuer(store, voucherTestWrapper{}, "https://vault.test/keys/voucher/1")
	service, _ := NewStaffService(issuer, &revokerStub{}, staffStepUpStub{})
	h.ConfigureStaff(service)
	limiter := &revealLimiterStub{}
	h.ConfigureRevealLimiter(limiter)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/vouchers/batches/33333333-3333-4333-8333-333333333333/reveal", strings.NewReader(`{}`))
	request.SetPathValue("id", "33333333-3333-4333-8333-333333333333")
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: voucherTestTenantID, UserID: "22222222-2222-4222-8222-222222222222"}))
	response := httptest.NewRecorder()
	h.revealBatch(response, request)
	if response.Code != http.StatusTooManyRequests || store.revealed != 0 {
		t.Fatalf("blocked reveal status=%d calls=%d", response.Code, store.revealed)
	}
	limiter.allowed = true
	request = httptest.NewRequest(http.MethodPost, "/api/v1/vouchers/batches/33333333-3333-4333-8333-333333333333/reveal", strings.NewReader(`{}`))
	request.SetPathValue("id", "33333333-3333-4333-8333-333333333333")
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: voucherTestTenantID, UserID: "22222222-2222-4222-8222-222222222222"}))
	response = httptest.NewRecorder()
	h.revealBatch(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || store.revealed != 1 || !strings.Contains(response.Body.String(), "NC-ABCDEF-GHJKLM-NPQRST") {
		t.Fatalf("reveal status=%d body=%s", response.Code, response.Body.String())
	}
}

const voucherTestTenantID = "11111111-1111-4111-8111-111111111111"

type memoryStore struct {
	tenantID string
	options  ListOptions
	page     Page
	err      error
}

func (s *memoryStore) List(_ context.Context, tenantID string, options ListOptions) (Page, error) {
	s.tenantID = tenantID
	s.options = options
	return s.page, s.err
}

func newTestHTTP(t *testing.T) (*HTTP, *memoryStore) {
	t.Helper()
	expiresAt := time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)
	store := &memoryStore{
		page: Page{
			Batches: []Batch{{
				ID:        "22222222-2222-4222-8222-222222222222",
				PlanName:  "Campus 24 hour",
				Issued:    1000,
				Redeemed:  861,
				Available: 139,
				Status:    "ACTIVE",
				ExpiresAt: &expiresAt,
				CreatedAt: time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
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
		CreatedAt: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC),
		ID:        "33333333-3333-4333-8333-333333333333",
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/vouchers/batches?limit=10&q=campus&cursor="+cursor, nil)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: voucherTestTenantID}))
	response := httptest.NewRecorder()

	handler.list(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	if store.tenantID != voucherTestTenantID || store.options.Limit != 10 || store.options.Search != "campus" || store.options.Cursor.ID != "33333333-3333-4333-8333-333333333333" {
		t.Fatalf("unexpected store request: tenant=%q options=%+v", store.tenantID, store.options)
	}
	var body listResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].Available != 139 || body.Data[0].PlanName != "Campus 24 hour" || body.Data[0].ExpiresAt == nil {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestListRejectsUnsafePageParameters(t *testing.T) {
	handler, _ := newTestHTTP(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/vouchers/batches?limit=101", nil)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: voucherTestTenantID}))
	response := httptest.NewRecorder()

	handler.list(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
}

func TestListRejectsMissingPrincipal(t *testing.T) {
	handler, _ := newTestHTTP(t)
	response := httptest.NewRecorder()

	handler.list(response, httptest.NewRequest(http.MethodGet, "/api/v1/vouchers/batches", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
}

func TestCursorRoundTripAndRejectsExtraFields(t *testing.T) {
	cursor := Cursor{
		CreatedAt: time.Date(2026, 8, 12, 10, 0, 0, 123, time.UTC),
		ID:        "44444444-4444-4444-8444-444444444444",
	}
	decoded, err := decodeCursor(encodeCursor(cursor))
	if err != nil || decoded != cursor {
		t.Fatalf("cursor = %+v, %v", decoded, err)
	}
	extra := base64.RawURLEncoding.EncodeToString([]byte(`{"created_at":"2026-08-12T10:00:00Z","id":"44444444-4444-4444-8444-444444444444","extra":true}`))
	if _, err := decodeCursor(extra); err == nil {
		t.Fatal("cursor with an unexpected field was accepted")
	}
}
