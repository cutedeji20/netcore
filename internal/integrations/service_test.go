package integrations

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
)

type memoryIntegrationStore struct {
	saved Record
	err   error
	items []Record
}

func (s *memoryIntegrationStore) Disable(_ context.Context, tenantID string, provider Provider, actorID string, at time.Time) error {
	if s.saved.TenantID == tenantID && s.saved.Provider == provider {
		s.saved.Status = StatusDisabled
		s.saved.UpdatedBy = actorID
		s.saved.UpdatedAt = at
	}
	return s.err
}

func (s *memoryIntegrationStore) Disconnect(_ context.Context, tenantID string, provider Provider, actorID string, at time.Time) error {
	if s.saved.TenantID == tenantID && s.saved.Provider == provider {
		s.saved.Status = StatusDisconnected
		s.saved.Envelope = CredentialEnvelope{}
		s.saved.UpdatedBy = actorID
		s.saved.UpdatedAt = at
	}
	return s.err
}

func (s *memoryIntegrationStore) Save(_ context.Context, record Record) error {
	s.saved = record
	s.items = []Record{record}
	return s.err
}

func (s *memoryIntegrationStore) List(_ context.Context, _ string) ([]Record, error) {
	return append([]Record(nil), s.items...), s.err
}

type testStepUpVerifier struct {
	err   error
	calls int
}

type rejectingProviderValidator struct{ err error }

func (v rejectingProviderValidator) Validate(context.Context, ConfigureInput) error { return v.err }

func (v *testStepUpVerifier) VerifyStepUp(_ context.Context, _ auth.StepUpInput) error {
	v.calls++
	return v.err
}

func TestConfigureResendPersistsOnlyAnEncryptedEnvelopeAfterStepUp(t *testing.T) {
	// This fails if a browser session can save an integration without fresh
	// password/TOTP verification, or if the store receives a plaintext key.
	store := &memoryIntegrationStore{}
	stepUp := &testStepUpVerifier{}
	service, err := NewService(store, testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, stepUp)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 8, 24, 13, 0, 0, 0, time.UTC) }

	credential := []byte("re_realistic_test_credential")
	principal := auth.Principal{TenantID: "tenant-a", UserID: "staff-a", Email: "admin@example.test"}
	err = service.Configure(context.Background(), ConfigureInput{
		Principal: principal, Password: "current password", MFACode: "123456",
		Provider: ProviderResend, Credential: credential, SenderEmail: "NetCore <access@example.test>",
	})
	if err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if stepUp.calls != 1 {
		t.Fatalf("step-up calls=%d, want 1", stepUp.calls)
	}
	if store.saved.TenantID != "tenant-a" || store.saved.Provider != ProviderResend || store.saved.Status != StatusActive {
		t.Fatalf("saved record = %#v", store.saved)
	}
	if store.saved.SenderEmail != "NetCore <access@example.test>" || !store.saved.ActivatedAt.Equal(service.now()) {
		t.Fatalf("saved safe metadata = %#v", store.saved)
	}
	if bytes.Contains(store.saved.Envelope.Ciphertext, credential) || bytes.Equal(store.saved.Envelope.WrappedDEK, credential) {
		t.Fatal("store received plaintext provider credential")
	}
}

func TestConfigureDoesNotEncryptOrPersistWhenStepUpFails(t *testing.T) {
	// This fails if an invalid step-up can overwrite the working credential.
	store := &memoryIntegrationStore{}
	stepUp := &testStepUpVerifier{err: auth.ErrInvalidCredentials}
	service, err := NewService(store, testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, stepUp)
	if err != nil {
		t.Fatal(err)
	}
	err = service.Configure(context.Background(), ConfigureInput{
		Principal: auth.Principal{TenantID: "tenant-a", UserID: "staff-a", Email: "admin@example.test"}, Password: "wrong", MFACode: "123456",
		Provider: ProviderPaystack, Credential: []byte("sk_test_credential"), PaystackMode: "TEST",
	})
	if !errors.Is(err, ErrStepUpFailed) {
		t.Fatalf("Configure error = %v, want step-up failed", err)
	}
	if len(store.saved.Envelope.Ciphertext) != 0 {
		t.Fatal("credential was persisted after failed step-up")
	}
}

func TestConfigureDoesNotPersistCredentialWhenProviderValidationFails(t *testing.T) {
	// This fails if an invalid Resend/Paystack key can be marked active before
	// the selected provider has accepted a bounded server-side check.
	store := &memoryIntegrationStore{}
	stepUp := &testStepUpVerifier{}
	service, err := NewService(store, testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, stepUp, rejectingProviderValidator{err: errors.New("provider rejected key")})
	if err != nil {
		t.Fatal(err)
	}
	err = service.Configure(context.Background(), ConfigureInput{
		Principal: auth.Principal{TenantID: "tenant-a", UserID: "staff-a", Email: "admin@example.test"}, Password: "current password", MFACode: "123456",
		Provider: ProviderResend, Credential: []byte("re_rejected_credential"), SenderEmail: "NetCore <access@example.test>",
	})
	if !errors.Is(err, ErrCredentialInvalid) || len(store.saved.Envelope.Ciphertext) != 0 {
		t.Fatalf("Configure error=%v saved=%#v", err, store.saved)
	}
}

func TestDisconnectRequiresStepUpAndClearsEnvelope(t *testing.T) {
	// This fails if a stale session can erase or deactivate an integration, or
	// if disconnect leaves recoverable provider credential material behind.
	store := &memoryIntegrationStore{saved: Record{TenantID: "tenant-a", Provider: ProviderPaystack, Status: StatusActive, Envelope: CredentialEnvelope{Ciphertext: []byte("ciphertext"), Nonce: make([]byte, 12), WrappedDEK: []byte("wrapped"), KEKKeyID: "https://vault.example/keys/integrations/1"}}}
	stepUp := &testStepUpVerifier{}
	service, err := NewService(store, testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, stepUp)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 8, 24, 14, 0, 0, 0, time.UTC) }
	principal := auth.Principal{TenantID: "tenant-a", UserID: "staff-a", Email: "admin@example.test"}
	if err := service.Disconnect(context.Background(), principal, "current", "123456", ProviderPaystack); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if stepUp.calls != 1 || store.saved.Status != StatusDisconnected || len(store.saved.Envelope.Ciphertext) != 0 || len(store.saved.Envelope.WrappedDEK) != 0 {
		t.Fatalf("disconnect state = %#v step-up=%d", store.saved, stepUp.calls)
	}
}
