package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/netcore-isp/netcore/internal/auth"
	"github.com/netcore-isp/netcore/internal/database"
	"github.com/netcore-isp/netcore/internal/logger"
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

func TestConfigureResendLogsPrivateFailureStagesWithoutSubmittedSecrets(t *testing.T) {
	// This fails if a Key Vault or database save failure is indistinguishable in
	// production logs, or if diagnostic logging leaks the submitted secret.
	cases := []struct {
		name      string
		wrapper   KeyWrapper
		storeErr  error
		wantStage string
		wantCause string
		forbidden string
	}{
		{name: "key vault", wrapper: failingIntegrationKeyWrapper{}, wantStage: "key_vault_wrap"},
		{name: "store precondition", wrapper: testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, storeErr: ErrStorePrecondition, wantStage: "store_precondition"},
		{name: "database upsert", wrapper: testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, storeErr: ErrStoreUpsert, wantStage: "database_upsert"},
		{name: "audit context cancellation", wrapper: testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, storeErr: fmt.Errorf("%w: %w", ErrStoreAudit, context.Canceled), wantStage: "audit_write", wantCause: "context_cancelled"},
		{name: "audit context deadline", wrapper: testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, storeErr: fmt.Errorf("%w: %w", ErrStoreAudit, context.DeadlineExceeded), wantStage: "audit_write", wantCause: "context_deadline"},
		{name: "audit PostgreSQL", wrapper: testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, storeErr: fmt.Errorf("%w: %w", ErrStoreAudit, &pgconn.PgError{Code: "23514", Message: "private PostgreSQL diagnostic"}), wantStage: "audit_write", wantCause: "postgres_sqlstate_23514", forbidden: "private PostgreSQL diagnostic"},
		{name: "audit malformed PostgreSQL code", wrapper: testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, storeErr: fmt.Errorf("%w: %w", ErrStoreAudit, &pgconn.PgError{Code: "23514 private diagnostic", Message: "private PostgreSQL diagnostic"}), wantStage: "audit_write", wantCause: "driver_or_transport", forbidden: "private PostgreSQL diagnostic"},
		{name: "audit driver", wrapper: testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, storeErr: fmt.Errorf("%w: %w", ErrStoreAudit, errors.New("private driver diagnostic")), wantStage: "audit_write", wantCause: "driver_or_transport", forbidden: "private driver diagnostic"},
		{name: "database transaction setup", wrapper: testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, storeErr: ErrStoreTxSetup, wantStage: "database_transaction_setup"},
		{name: "database transaction commit", wrapper: testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, storeErr: ErrStoreTxCommit, wantStage: "database_transaction_commit"},
		{name: "database fallback", wrapper: testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, storeErr: errors.New("database write failed"), wantStage: "database_save"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(logger.New(&logs, logger.Options{ServiceName: "test", Env: "test"}))
			t.Cleanup(func() { slog.SetDefault(previous) })

			store := &memoryIntegrationStore{err: tc.storeErr}
			service, err := NewService(store, tc.wrapper, &testStepUpVerifier{})
			if err != nil {
				t.Fatal(err)
			}
			handler, err := NewHTTP(service)
			if err != nil {
				t.Fatal(err)
			}

			const credential = "re_submitted_credential_must_not_appear"
			const password = "submitted-password-must-not-appear"
			const mfaCode = "654321"
			request := integrationPrincipalRequest(http.MethodPut, "/api/v1/integrations/resend", []byte(`{"credential":"`+credential+`","sender_email":"access@example.test","password":"`+password+`","mfa_code":"`+mfaCode+`"}`))
			request = request.WithContext(logger.WithRequestID(request.Context(), "integration-diagnostic-1"))
			response := httptest.NewRecorder()

			handler.configureResend(response, request)

			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			for _, forbidden := range []string{credential, password, mfaCode} {
				if strings.Contains(response.Body.String(), forbidden) {
					t.Fatalf("response exposed submitted value %q: %s", forbidden, response.Body)
				}
			}

			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatalf("diagnostic log=%q: %v", logs.String(), err)
			}
			if entry["msg"] != "integration configuration failed" || entry["failure_stage"] != tc.wantStage || entry["request_id"] != "integration-diagnostic-1" {
				t.Fatalf("diagnostic entry=%#v", entry)
			}
			if tc.wantCause != "" && entry["failure_cause"] != tc.wantCause {
				t.Fatalf("failure_cause=%#v want %q; entry=%#v", entry["failure_cause"], tc.wantCause, entry)
			}
			forbiddenValues := []string{credential, password, mfaCode, "database write failed"}
			if tc.forbidden != "" {
				forbiddenValues = append(forbiddenValues, tc.forbidden)
			}
			for _, forbidden := range forbiddenValues {
				if strings.Contains(logs.String(), forbidden) {
					t.Fatalf("diagnostic log exposed %q: %s", forbidden, logs.String())
				}
			}
		})
	}
}

func TestConfigureResendLabelsTenantTransactionSetupFailure(t *testing.T) {
	// This fails if the real PostgreSQL store collapses a transaction setup
	// failure into the generic database_save diagnostic stage.
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(logger.New(&logs, logger.Options{ServiceName: "test", Env: "test"}))
	t.Cleanup(func() { slog.SetDefault(previous) })

	service, err := NewService(&PostgresStore{db: &database.Pool{}}, testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, &testStepUpVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTP(service)
	if err != nil {
		t.Fatal(err)
	}

	request := integrationPrincipalRequest(http.MethodPut, "/api/v1/integrations/resend", []byte(`{"credential":"re_transaction_setup_test","sender_email":"access@example.test","password":"current","mfa_code":"123456"}`))
	request = request.WithContext(logger.WithRequestID(request.Context(), "integration-transaction-setup-1"))
	response := httptest.NewRecorder()
	handler.configureResend(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("diagnostic log=%q: %v", logs.String(), err)
	}
	if entry["failure_stage"] != "database_transaction_setup" || entry["request_id"] != "integration-transaction-setup-1" {
		t.Fatalf("diagnostic entry=%#v", entry)
	}
}

type failingIntegrationKeyWrapper struct{}

func (failingIntegrationKeyWrapper) Wrap(context.Context, []byte) (WrappedDEK, error) {
	return WrappedDEK{}, ErrKeyUnavailable
}

func (failingIntegrationKeyWrapper) Unwrap(context.Context, []byte, string) ([]byte, error) {
	return nil, ErrKeyUnavailable
}
