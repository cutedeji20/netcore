package devices

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/netcore-isp/netcore/internal/auth"
)

const testTenant = "11111111-1111-4111-8111-111111111111"
const testUser = "22222222-2222-4222-8222-222222222222"

type memoryStore struct {
	tenantID, userID string
	registered       Registration
	values           []Device
	err              error
}

func (s *memoryStore) List(_ context.Context, tenantID, userID string) ([]Device, error) {
	s.tenantID, s.userID = tenantID, userID
	return s.values, s.err
}
func (s *memoryStore) Register(_ context.Context, tenantID, userID string, input Registration) (Device, error) {
	s.tenantID, s.userID, s.registered = tenantID, userID, input
	if s.err != nil {
		return Device{}, s.err
	}
	return Device{ID: "44444444-4444-4444-8444-444444444444", NormalizedMAC: input.NormalizedMAC, Label: input.Label, Status: "ACTIVE"}, nil
}

func TestDeviceHTTPRegistersOnlyForAuthenticatedCustomer(t *testing.T) {
	store := &memoryStore{}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHTTP(service)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/portal/devices", strings.NewReader(`{"mac":"AA:BB:CC:DD:EE:FF","label":"Laptop","customer_id":"attacker"}`))
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: testTenant, UserID: testUser}))
	response := httptest.NewRecorder()
	h.register(response, request)
	if response.Code != http.StatusBadRequest || store.tenantID != "" {
		t.Fatalf("status=%d scope=%q/%q", response.Code, store.tenantID, store.userID)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/portal/devices", strings.NewReader(`{"mac":"AA:BB:CC:DD:EE:FF","label":"Laptop"}`))
	request = request.WithContext(auth.ContextWithPrincipal(request.Context(), auth.Principal{TenantID: testTenant, UserID: testUser}))
	response = httptest.NewRecorder()
	h.register(response, request)
	if response.Code != http.StatusCreated || store.tenantID != testTenant || store.userID != testUser || store.registered.NormalizedMAC != "aabbccddeeff" {
		t.Fatalf("status=%d scope=%q/%q registration=%+v", response.Code, store.tenantID, store.userID, store.registered)
	}
	raw := response.Body.String()
	if !strings.Contains(raw, `"normalized_mac":"aabbccddeeff"`) || strings.Contains(raw, `"NormalizedMAC"`) {
		t.Fatalf("unsafe device JSON projection: %s", raw)
	}
	var body struct {
		Data Device `json:"data"`
	}
	if err := json.NewDecoder(strings.NewReader(raw)).Decode(&body); err != nil || body.Data.ID == "" || body.Data.NormalizedMAC != "aabbccddeeff" {
		t.Fatalf("response=%s decoded=%+v err=%v", raw, body, err)
	}
}
