package subscriptions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
)

const subscriptionTestTenantID = "11111111-1111-4111-8111-111111111111"

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
	startsAt := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	store := &memoryStore{
		page: Page{
			Subscriptions: []Subscription{{
				ID:                "22222222-2222-4222-8222-222222222222",
				CustomerID:        "33333333-3333-4333-8333-333333333333",
				CustomerNumber:    "CUS-10482",
				CustomerFirstName: "Chika",
				CustomerLastName:  "Nwosu",
				PlanID:            "44444444-4444-4444-8444-444444444444",
				PlanName:          "Home Pro 50",
				Status:            StatusActive,
				StartsAt:          &startsAt,
				ExpiresAt:         &expiresAt,
				AutoRenew:         true,
				PaymentStatus:     "PAID",
				CreatedAt:         time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
				UpdatedAt:         time.Date(2026, 8, 12, 10, 5, 0, 0, time.UTC),
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
		ID:        "55555555-5555-4555-8555-555555555555",
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions?limit=10&q=chika&status=ACTIVE&cursor="+cursor, nil)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: subscriptionTestTenantID}))
	response := httptest.NewRecorder()

	handler.list(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	if store.tenantID != subscriptionTestTenantID || store.options.Limit != 10 || store.options.Search != "chika" || store.options.Status != StatusActive || store.options.Cursor.ID != "55555555-5555-4555-8555-555555555555" {
		t.Fatalf("unexpected store request: tenant=%q options=%+v", store.tenantID, store.options)
	}
	var body listResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].Plan.Name != "Home Pro 50" || body.Data[0].Customer.CustomerNumber != "CUS-10482" || body.Data[0].ExpiresAt == nil {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestListRejectsUnsafePageParameters(t *testing.T) {
	handler, _ := newTestHTTP(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions?status=UNKNOWN", nil)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: subscriptionTestTenantID}))
	response := httptest.NewRecorder()

	handler.list(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
}

func TestListRejectsMissingPrincipal(t *testing.T) {
	handler, _ := newTestHTTP(t)
	response := httptest.NewRecorder()

	handler.list(response, httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
}

func TestCursorRoundTripAndRejectsExtraFields(t *testing.T) {
	cursor := Cursor{
		CreatedAt: time.Date(2026, 8, 12, 10, 0, 0, 123, time.UTC),
		ID:        "66666666-6666-4666-8666-666666666666",
	}
	decoded, err := decodeCursor(encodeCursor(cursor))
	if err != nil || decoded != cursor {
		t.Fatalf("cursor = %+v, %v", decoded, err)
	}
	extra := base64.RawURLEncoding.EncodeToString([]byte(`{"created_at":"2026-08-12T10:00:00Z","id":"66666666-6666-4666-8666-666666666666","extra":true}`))
	if _, err := decodeCursor(extra); err == nil {
		t.Fatal("cursor with an unexpected field was accepted")
	}
}
