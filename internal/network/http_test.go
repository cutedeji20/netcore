package network

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

const networkTestTenantID = "11111111-1111-4111-8111-111111111111"

type memoryStore struct {
	tenantID string
	options  ListOptions
	page     Page
	err      error
}

type memoryTetheringStore struct {
	policy TetheringPolicy
	actor  MutationActor
	saved  bool
}

func (s *memoryTetheringStore) LoadTetheringPolicy(context.Context, string) (TetheringPolicy, error) {
	return s.policy, nil
}
func (s *memoryTetheringStore) SaveTetheringPolicy(_ context.Context, _ string, actor MutationActor, policy TetheringPolicy) error {
	s.policy, s.actor, s.saved = policy, actor, true
	return nil
}

func TestTetheringPolicyRequiresNetworkWriteAuditsAndBoundsTTL(t *testing.T) {
	store := &memoryTetheringStore{}
	service, err := NewTetheringService(store)
	if err != nil {
		t.Fatal(err)
	}
	handler, _ := newTestHTTP(t)
	handler.ConfigureTethering(service)
	principal := auth.Principal{TenantID: networkTestTenantID, UserID: "44444444-4444-4444-8444-444444444444", Permissions: map[string]struct{}{"network.write": {}}}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/network/tethering-policy", strings.NewReader(`{"enabled":true,"expected_client_ttl":64}`))
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), principal))
	response := httptest.NewRecorder()
	handler.putTetheringPolicy(response, request)
	if response.Code != http.StatusOK || !store.saved || !store.policy.Enabled || store.policy.ExpectedClientTTL != 64 || store.actor.UserID != principal.UserID {
		t.Fatalf("status=%d policy=%+v actor=%+v", response.Code, store.policy, store.actor)
	}
	request = httptest.NewRequest(http.MethodPut, "/api/v1/network/tethering-policy", strings.NewReader(`{"enabled":true,"expected_client_ttl":256}`))
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), principal))
	response = httptest.NewRecorder()
	handler.putTetheringPolicy(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("ttl above range status=%d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPut, "/api/v1/network/tethering-policy", strings.NewReader(`{"enabled":true,"expected_client_ttl":64}`))
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: networkTestTenantID, UserID: principal.UserID}))
	response = httptest.NewRecorder()
	handler.putTetheringPolicy(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing permission status=%d", response.Code)
	}
}

func TestExportReturnsNoStoreAttachmentWithoutSecretInErrors(t *testing.T) {
	store := &lifecycleStoreStub{loaded: AAAConfiguration{RouterID: "22222222-2222-4222-8222-222222222222", NASID: "33333333-3333-4333-8333-333333333333", NASIPAddress: "172.16.0.4", RadiusSourceIP: "172.16.1.9", ShortName: "RB5009-LK-01"}}
	service, err := NewService(store, credentialTestWrapper{keyID: "https://vault.example/keys/netcore-provider-kek/version"}, stepUpStub{}, "172.16.0.4")
	if err != nil {
		t.Fatal(err)
	}
	handler, _ := newTestHTTP(t)
	handler.ConfigureLifecycle(service)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/network/routers/22222222-2222-4222-8222-222222222222/aaa/export", strings.NewReader(`{"password":"current","mfa_code":"123456"}`))
	request.SetPathValue("routerID", "22222222-2222-4222-8222-222222222222")
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: networkTestTenantID, UserID: "44444444-4444-4444-8444-444444444444", Permissions: map[string]struct{}{"network.write": {}}}))
	response := httptest.NewRecorder()

	handler.export(response, request)

	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("response status=%d headers=%v", response.Code, response.Header())
	}
}

func TestAAAStatusOmitsSecretMaterial(t *testing.T) {
	store := &lifecycleStoreStub{loaded: AAAConfiguration{RouterID: "22222222-2222-4222-8222-222222222222", NASID: "33333333-3333-4333-8333-333333333333", NASIPAddress: "172.16.0.4", RadiusSourceIP: "172.16.1.9", ShortName: "RB5009-LK-01", Status: NASStatusDisabled, Version: 2}}
	service, _ := NewService(store, credentialTestWrapper{keyID: "https://vault.example/keys/netcore-provider-kek/version"}, stepUpStub{}, "172.16.0.4")
	handler, _ := newTestHTTP(t)
	handler.ConfigureLifecycle(service)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/network/routers/22222222-2222-4222-8222-222222222222/aaa", nil)
	request.SetPathValue("routerID", "22222222-2222-4222-8222-222222222222")
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: networkTestTenantID, Permissions: map[string]struct{}{"network.read": {}}}))
	response := httptest.NewRecorder()
	handler.aaa(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "secret") || !strings.Contains(response.Body.String(), `"status":"DISABLED"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestRouterMutationHandlersRejectTrailingJSON(t *testing.T) {
	store := &lifecycleStoreStub{loaded: AAAConfiguration{RouterID: "22222222-2222-4222-8222-222222222222", NASID: "33333333-3333-4333-8333-333333333333", NASIPAddress: "172.16.0.4", RadiusSourceIP: "172.16.1.9", ShortName: "RB5009-LK-01"}}
	service, err := NewService(store, credentialTestWrapper{keyID: "https://vault.example/keys/netcore-provider-kek/version"}, stepUpStub{}, "172.16.0.4")
	if err != nil {
		t.Fatal(err)
	}
	handler, _ := newTestHTTP(t)
	handler.ConfigureLifecycle(service)
	principal := auth.Principal{TenantID: networkTestTenantID, UserID: "44444444-4444-4444-8444-444444444444", Permissions: map[string]struct{}{"network.write": {}}}

	for name, test := range map[string]struct {
		target string
		body   string
		invoke func(http.ResponseWriter, *http.Request)
	}{
		"create": {"/api/v1/network/routers", `{"name":"RB5009","management_ip":"172.16.0.4","nas_ip_address":"172.16.0.4","radius_source_ip":"172.16.0.4","password":"current","mfa_code":"123456"}{}`, handler.createRouter},
		"status": {"/api/v1/network/routers/22222222-2222-4222-8222-222222222222/aaa/activate", `{"password":"current","mfa_code":"123456"}{}`, handler.changeNASStatus(NASStatusActive)},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.target, strings.NewReader(test.body))
			request.SetPathValue("routerID", "22222222-2222-4222-8222-222222222222")
			request = request.WithContext(auth.ContextWithPrincipal(request.Context(), principal))
			response := httptest.NewRecorder()
			test.invoke(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
		})
	}
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
