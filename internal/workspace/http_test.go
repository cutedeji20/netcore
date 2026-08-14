package workspace

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

const workspaceTestTenantID = "11111111-1111-4111-8111-111111111111"

type memoryStore struct {
	tenantID string
	snapshot Snapshot
	err      error
}

func (s *memoryStore) Get(_ context.Context, tenantID string) (Snapshot, error) {
	s.tenantID = tenantID
	return s.snapshot, s.err
}

func newTestHTTP(t *testing.T) (*HTTP, *memoryStore) {
	t.Helper()
	store := &memoryStore{snapshot: Snapshot{
		Name:              "Lagos Hub",
		Slug:              "lagos-hub",
		Timezone:          "Africa/Lagos",
		Currency:          "NGN",
		Status:            "ACTIVE",
		UpdatedAt:         time.Date(2026, 8, 12, 13, 18, 0, 0, time.UTC),
		RegisteredRouters: 18,
		ActiveTeamMembers: 8,
	}}
	handler, err := NewHTTP(store)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store
}

func TestGetUsesPrincipalTenant(t *testing.T) {
	handler, store := newTestHTTP(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/workspace/settings", nil)
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: workspaceTestTenantID}))
	response := httptest.NewRecorder()

	handler.get(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	if store.tenantID != workspaceTestTenantID {
		t.Fatalf("store tenant = %q", store.tenantID)
	}
	var body responseSnapshot
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Name != "Lagos Hub" || body.Timezone != "Africa/Lagos" || body.RegisteredRouters != 18 || body.ActiveTeamMembers != 8 {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestGetRejectsMissingPrincipal(t *testing.T) {
	handler, _ := newTestHTTP(t)
	response := httptest.NewRecorder()

	handler.get(response, httptest.NewRequest(http.MethodGet, "/api/v1/workspace/settings", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
}

func TestWorkspaceResponseExcludesSensitiveConfiguration(t *testing.T) {
	encoded, err := json.Marshal(toResponseSnapshot(Snapshot{Name: "Lagos Hub"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"credential", "secret", "api_key", "api_endpoint", "management_ip", "tenant_id"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("response exposed %q: %s", forbidden, encoded)
		}
	}
}
