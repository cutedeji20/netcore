package network

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

const networkTestTenantID = "11111111-1111-4111-8111-111111111111"

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
	lastSeenAt := time.Date(2026, 8, 12, 13, 14, 0, 0, time.UTC)
	store := &memoryStore{
		page: Page{
			Routers: []Router{{
				ID:         "22222222-2222-4222-8222-222222222222",
				Name:       "RB5009-LK-01",
				SiteName:   "Lekki Phase 1",
				Status:     RouterStatusOnline,
				AAAStatus:  "ACTIVE",
				LastSeenAt: &lastSeenAt,
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
	cursor := encodeCursor(Cursor{Name: "RB5009-VI-01"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/network/routers?limit=10&q=lekki&status=ONLINE&cursor="+cursor, nil)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: networkTestTenantID}))
	response := httptest.NewRecorder()

	handler.list(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	if store.tenantID != networkTestTenantID || store.options.Limit != 10 || store.options.Search != "lekki" || store.options.Status != RouterStatusOnline || store.options.Cursor.Name != "RB5009-VI-01" {
		t.Fatalf("unexpected store request: tenant=%q options=%+v", store.tenantID, store.options)
	}
	var body listResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].AAAStatus != "ACTIVE" || body.Data[0].SiteName != "Lekki Phase 1" || body.Data[0].LastSeenAt == nil {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestListRejectsUnsafePageParameters(t *testing.T) {
	handler, _ := newTestHTTP(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/network/routers?status=UNKNOWN", nil)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: networkTestTenantID}))
	response := httptest.NewRecorder()

	handler.list(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
}

func TestListRejectsMissingPrincipal(t *testing.T) {
	handler, _ := newTestHTTP(t)
	response := httptest.NewRecorder()

	handler.list(response, httptest.NewRequest(http.MethodGet, "/api/v1/network/routers", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
}

func TestCursorRoundTripAndRejectsExtraFields(t *testing.T) {
	cursor := Cursor{Name: "CCR2004-YB-01"}
	decoded, err := decodeCursor(encodeCursor(cursor))
	if err != nil || decoded != cursor {
		t.Fatalf("cursor = %+v, %v", decoded, err)
	}
	extra := base64.RawURLEncoding.EncodeToString([]byte(`{"name":"CCR2004-YB-01","extra":true}`))
	if _, err := decodeCursor(extra); err == nil {
		t.Fatal("cursor with an unexpected field was accepted")
	}
}
