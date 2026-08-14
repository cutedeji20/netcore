package security

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const activityTestTenantID = "11111111-1111-4111-8111-111111111111"

type activityTestTenantKey struct{}

type activityMemoryStore struct {
	tenantID string
	options  ActivityListOptions
	page     ActivityPage
	err      error
}

func (s *activityMemoryStore) ListActivity(_ context.Context, tenantID string, options ActivityListOptions) (ActivityPage, error) {
	s.tenantID = tenantID
	s.options = options
	return s.page, s.err
}

func newTestActivityHTTP(t *testing.T) (*ActivityHTTP, *activityMemoryStore) {
	t.Helper()
	occurredAt := time.Date(2026, 8, 12, 13, 18, 0, 0, time.UTC)
	store := &activityMemoryStore{
		page: ActivityPage{
			Events: []ActivityEvent{{
				ID:           "22222222-2222-4222-8222-222222222222",
				Action:       "SESSION_REVOKED",
				Actor:        "amara@example.test",
				ResourceType: "Browser session",
				CreatedAt:    occurredAt,
			}},
		},
	}
	handler, err := NewActivityHTTP(store, func(ctx context.Context) (string, bool) {
		tenantID, ok := ctx.Value(activityTestTenantKey{}).(string)
		return tenantID, ok
	}, 25, 100)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store
}

func TestActivityListUsesTenantScopedCursorOptions(t *testing.T) {
	handler, store := newTestActivityHTTP(t)
	cursor := encodeActivityCursor(ActivityCursor{
		CreatedAt: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC),
		ID:        "33333333-3333-4333-8333-333333333333",
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/security/events?limit=10&q=session&cursor="+cursor, nil)
	request = request.WithContext(context.WithValue(request.Context(), activityTestTenantKey{}, activityTestTenantID))
	response := httptest.NewRecorder()

	handler.list(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	if store.tenantID != activityTestTenantID || store.options.Limit != 10 || store.options.Search != "session" || store.options.Cursor.ID != "33333333-3333-4333-8333-333333333333" {
		t.Fatalf("unexpected store request: tenant=%q options=%+v", store.tenantID, store.options)
	}
	var body activityListResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].Action != "SESSION_REVOKED" || body.Data[0].Actor != "amara@example.test" || body.Data[0].ResourceType != "Browser session" {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestActivityListRejectsUnsafePageParameters(t *testing.T) {
	handler, _ := newTestActivityHTTP(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/security/events?limit=101", nil)
	request = request.WithContext(context.WithValue(request.Context(), activityTestTenantKey{}, activityTestTenantID))
	response := httptest.NewRecorder()

	handler.list(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
}

func TestActivityListRejectsMissingPrincipal(t *testing.T) {
	handler, _ := newTestActivityHTTP(t)
	response := httptest.NewRecorder()

	handler.list(response, httptest.NewRequest(http.MethodGet, "/api/v1/security/events", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
}

func TestActivityCursorRoundTripAndRejectsExtraFields(t *testing.T) {
	cursor := ActivityCursor{
		CreatedAt: time.Date(2026, 8, 12, 10, 0, 0, 123, time.UTC),
		ID:        "44444444-4444-4444-8444-444444444444",
	}
	decoded, err := decodeActivityCursor(encodeActivityCursor(cursor))
	if err != nil || decoded != cursor {
		t.Fatalf("cursor = %+v, %v", decoded, err)
	}
	extra := base64.RawURLEncoding.EncodeToString([]byte("{\"created_at\":\"2026-08-12T10:00:00Z\",\"id\":\"44444444-4444-4444-8444-444444444444\",\"extra\":true}"))
	if _, err := decodeActivityCursor(extra); err == nil {
		t.Fatal("cursor with an unexpected field was accepted")
	}
}

func TestActivityResponseExcludesForensicFields(t *testing.T) {
	encoded, err := json.Marshal(responseActivityEvent(ActivityEvent{
		ID:           "55555555-5555-4555-8555-555555555555",
		Action:       "LOGIN_SUCCESS",
		Actor:        "operator@example.test",
		ResourceType: "Account",
		CreatedAt:    time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"ip_address", "user_agent", "request_id", "metadata", "actor_id", "resource_id"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("response exposed %q: %s", forbidden, encoded)
		}
	}
}
