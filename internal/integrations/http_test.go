package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
)

func newTestIntegrationHTTP(t *testing.T) (*HTTP, *memoryIntegrationStore) {
	t.Helper()
	store := &memoryIntegrationStore{items: []Record{{
		TenantID: "tenant-a", Provider: ProviderResend, Status: StatusActive,
		Envelope:    CredentialEnvelope{Ciphertext: []byte("not-a-secret-response"), Nonce: make([]byte, 12), WrappedDEK: []byte("wrapped"), KEKKeyID: "https://vault.example/keys/integrations/1"},
		SenderEmail: "NetCore <access@example.test>", ActivatedAt: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
	}}}
	service, err := NewService(store, testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, &testStepUpVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTP(service)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store
}

func integrationPrincipalRequest(method, path string, body []byte) *http.Request {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	return request.WithContext(auth.ContextWithPrincipal(context.Background(), auth.Principal{TenantID: "tenant-a", UserID: "staff-a", Email: "admin@example.test", Permissions: map[string]struct{}{"integration.read": {}, "integration.write": {}}}))
}

func TestListReturnsOnlySafeIntegrationMetadata(t *testing.T) {
	// This fails if any database envelope field reaches an authenticated browser.
	handler, _ := newTestIntegrationHTTP(t)
	response := httptest.NewRecorder()
	handler.list(response, integrationPrincipalRequest(http.MethodGet, "/api/v1/integrations", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"ciphertext", "nonce", "wrapped", "kek", "not-a-secret-response"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("integration response exposed %q: %s", forbidden, encoded)
		}
	}
}

func TestListUsesDashboardMetadataFieldNames(t *testing.T) {
	// The live dashboard consumes snake_case fields, and must never need to
	// infer provider state from implementation-specific Go field names.
	handler, _ := newTestIntegrationHTTP(t)
	response := httptest.NewRecorder()
	handler.list(response, integrationPrincipalRequest(http.MethodGet, "/api/v1/integrations", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var body struct {
		Integrations []map[string]any `json:"integrations"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Integrations) != 1 {
		t.Fatalf("integrations=%#v", body.Integrations)
	}
	metadata := body.Integrations[0]
	if got, want := metadata["sender_email"], "NetCore <access@example.test>"; got != want {
		t.Fatalf("sender_email=%#v want %#v; metadata=%#v", got, want, metadata)
	}
	if _, found := metadata["SenderEmail"]; found {
		t.Fatalf("dashboard metadata used Go field name: %#v", metadata)
	}
}

func TestConfigureResendHTTPRequiresAuthenticatedPrincipal(t *testing.T) {
	// This fails if a public endpoint can begin a credential configuration.
	handler, _ := newTestIntegrationHTTP(t)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/integrations/resend", strings.NewReader(`{"credential":"re_test","sender_email":"access@example.test","password":"current","mfa_code":"123456"}`))
	handler.configureResend(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestConfigurePaystackHTTPPersistsTestModeWithoutReturningCredential(t *testing.T) {
	// This fails if Paystack keys bypass the same step-up/envelope path or the
	// configuration endpoint reflects a submitted provider key.
	handler, store := newTestIntegrationHTTP(t)
	response := httptest.NewRecorder()
	request := integrationPrincipalRequest(http.MethodPut, "/api/v1/integrations/paystack", []byte(`{"credential":"sk_test_private_value","mode":"TEST","password":"current","mfa_code":"123456"}`))
	handler.configurePaystack(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if store.saved.Provider != ProviderPaystack || store.saved.PaystackMode != "TEST" {
		t.Fatalf("saved Paystack configuration = %#v", store.saved)
	}
	if strings.Contains(response.Body.String(), "sk_test_private_value") {
		t.Fatalf("response exposed submitted credential: %s", response.Body)
	}
}

func TestDisconnectHTTPClearsCredentialAfterStepUp(t *testing.T) {
	// This fails if the HTTP endpoint can report a disconnect without clearing
	// the stored encrypted provider material.
	handler, store := newTestIntegrationHTTP(t)
	store.saved = store.items[0]
	response := httptest.NewRecorder()
	request := integrationPrincipalRequest(http.MethodDelete, "/api/v1/integrations/resend", []byte(`{"password":"current","mfa_code":"123456"}`))
	handler.disconnect(ProviderResend)(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if store.saved.Status != StatusDisconnected || len(store.saved.Envelope.Ciphertext) != 0 {
		t.Fatalf("disconnect did not clear envelope: %#v", store.saved)
	}
}
