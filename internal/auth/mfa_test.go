package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/pkg/crypto/envelope"
	"github.com/netcore-isp/netcore/pkg/crypto/totp"
)

type memoryMFAStore struct {
	device   TOTPDevice
	found    bool
	consumed map[int64]bool
	err      error
}

func (s *memoryMFAStore) ActiveTOTPDevice(_ context.Context, tenantID, userID string) (TOTPDevice, bool, error) {
	if s.err != nil {
		return TOTPDevice{}, false, s.err
	}
	if !s.found || s.device.TenantID != tenantID || s.device.UserID != userID {
		return TOTPDevice{}, false, nil
	}
	return s.device, true, nil
}

func (s *memoryMFAStore) ConsumeTOTPCounter(_ context.Context, _ TOTPDevice, counter int64) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	if s.consumed[counter] {
		return false, nil
	}
	s.consumed[counter] = true
	return true, nil
}

type memorySecrets map[string]string

func (s memorySecrets) Resolve(_ context.Context, reference string) (string, error) {
	secret, ok := s[reference]
	if !ok {
		return "", errors.New("not found")
	}
	return secret, nil
}

type testLegacyResolver struct{}

func (testLegacyResolver) Resolve(_ context.Context, _ string) (string, error) {
	return "", errors.New("legacy resolver must not be used")
}

type testWrapper struct{}

func (testWrapper) Wrap(_ context.Context, dek []byte) (envelope.WrappedKey, error) {
	return envelope.WrappedKey{Ciphertext: append([]byte(nil), dek...), KeyID: "https://vault.example/keys/mfa/version"}, nil
}

func (testWrapper) Unwrap(_ context.Context, wrapped []byte, keyID string) ([]byte, error) {
	if keyID != "https://vault.example/keys/mfa/version" {
		return nil, errors.New("unexpected key ID")
	}
	return append([]byte(nil), wrapped...), nil
}

type failingUnwrapWrapper struct{}

func (failingUnwrapWrapper) Wrap(_ context.Context, _ []byte) (envelope.WrappedKey, error) {
	return envelope.WrappedKey{}, errors.New("key wrapper is unavailable")
}

func (failingUnwrapWrapper) Unwrap(_ context.Context, _ []byte, _ string) ([]byte, error) {
	return nil, errors.New("key wrapper is unavailable")
}

func mustSeal(t *testing.T, secret string) MFASecretEnvelope {
	t.Helper()
	value, err := SealTOTPSecret(context.Background(), testWrapper{}, "tenant-a", "user-mfa-totp", "user-a", secret)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestMFAServiceVerifiesDynamicEnvelopeWithoutSecretReference(t *testing.T) {
	// This fails if a newly enrolled device depends on the legacy secret store.
	secret := "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	store := &memoryMFAStore{device: TOTPDevice{TenantID: "tenant-a", UserID: "user-a", Envelope: mustSeal(t, secret)}, found: true, consumed: make(map[int64]bool)}
	service, err := NewMFAService(store, testLegacyResolver{}, testWrapper{})
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.Code(secret, time.Now(), totp.DefaultDigits)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyTOTP(context.Background(), "tenant-a", "user-a", code); err != nil {
		t.Fatal(err)
	}
}

func TestMFAServiceStillVerifiesLegacySecretReference(t *testing.T) {
	// This fails if bootstrap devices become unusable while dynamic MFA rolls out.
	secret := "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	store := &memoryMFAStore{device: TOTPDevice{TenantID: "tenant-a", UserID: "user-a", SecretRef: "netcore/mfa/totp/user-a"}, found: true, consumed: make(map[int64]bool)}
	service, err := NewMFAService(store, memorySecrets{store.device.SecretRef: secret}, testWrapper{})
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.Code(secret, time.Now(), totp.DefaultDigits)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyTOTP(context.Background(), "tenant-a", "user-a", code); err != nil {
		t.Fatal(err)
	}
}

func TestMFAServiceVerifiesLegacySecretReferenceWhenWrapperIsUnavailable(t *testing.T) {
	// This fails if a Key Vault outage or disabled wrapper is allowed to block
	// a bootstrap device that still uses the legacy secret-reference path.
	secret := "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	store := &memoryMFAStore{device: TOTPDevice{TenantID: "tenant-a", UserID: "user-a", SecretRef: "netcore/mfa/totp/user-a"}, found: true, consumed: make(map[int64]bool)}
	service, err := NewMFAService(store, memorySecrets{store.device.SecretRef: secret}, failingUnwrapWrapper{})
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.Code(secret, time.Now(), totp.DefaultDigits)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyTOTP(context.Background(), "tenant-a", "user-a", code); err != nil {
		t.Fatalf("VerifyTOTP: %v", err)
	}
}

func TestMFAServiceRejectsMalformedDynamicSecretRepresentations(t *testing.T) {
	// This fails if a corrupted dynamic representation can fall back to a
	// legacy reference or if a mixed representation is accepted.
	secret := "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	complete := mustSeal(t, secret)
	code, err := totp.Code(secret, time.Now(), totp.DefaultDigits)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		device TOTPDevice
	}{
		{name: "partial envelope with legacy reference", device: TOTPDevice{TenantID: "tenant-a", UserID: "user-a", SecretRef: "netcore/mfa/totp/user-a", Envelope: MFASecretEnvelope{Ciphertext: []byte{1}}}},
		{name: "complete envelope with legacy reference", device: TOTPDevice{TenantID: "tenant-a", UserID: "user-a", SecretRef: "netcore/mfa/totp/user-a", Envelope: complete}},
		{name: "no representation", device: TOTPDevice{TenantID: "tenant-a", UserID: "user-a"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &memoryMFAStore{device: tt.device, found: true, consumed: make(map[int64]bool)}
			service, err := NewMFAService(store, memorySecrets{tt.device.SecretRef: secret}, testWrapper{})
			if err != nil {
				t.Fatal(err)
			}
			if err := service.VerifyTOTP(context.Background(), "tenant-a", "user-a", code); !errors.Is(err, ErrInvalidMFA) {
				t.Fatalf("VerifyTOTP = %v, want ErrInvalidMFA", err)
			}
		})
	}
}

func TestMFAServiceRejectsEnvelopeCopiedToAnotherSubject(t *testing.T) {
	// This fails if ciphertext can be replayed under a different tenant or user.
	secret := "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	value := mustSeal(t, secret)
	code, err := totp.Code(secret, time.Now(), totp.DefaultDigits)
	if err != nil {
		t.Fatal(err)
	}
	for _, device := range []TOTPDevice{
		{TenantID: "tenant-b", UserID: "user-a", Envelope: value},
		{TenantID: "tenant-a", UserID: "user-b", Envelope: value},
	} {
		store := &memoryMFAStore{device: device, found: true, consumed: make(map[int64]bool)}
		service, err := NewMFAService(store, testLegacyResolver{}, testWrapper{})
		if err != nil {
			t.Fatal(err)
		}
		if err := service.VerifyTOTP(context.Background(), device.TenantID, device.UserID, code); !errors.Is(err, ErrInvalidMFA) {
			t.Fatalf("VerifyTOTP = %v, want ErrInvalidMFA", err)
		}
	}
}

func newTestMFAService(t *testing.T) (*MFAService, *memoryMFAStore, string) {
	t.Helper()
	secret, err := totp.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryMFAStore{
		device: TOTPDevice{
			ID:        "22222222-2222-4222-8222-222222222222",
			TenantID:  testTenantID,
			UserID:    "33333333-3333-4333-8333-333333333333",
			SecretRef: "netcore/mfa/totp/test-user",
		},
		found:    true,
		consumed: make(map[int64]bool),
	}
	service, err := NewMFAService(store, memorySecrets{store.device.SecretRef: secret}, testWrapper{})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC) }
	return service, store, secret
}

func TestVerifyTOTPConsumesTheMatchedCounter(t *testing.T) {
	service, _, secret := newTestMFAService(t)
	code, err := totp.Code(secret, service.now(), totp.DefaultDigits)
	if err != nil {
		t.Fatal(err)
	}
	userID := "33333333-3333-4333-8333-333333333333"
	if err := service.VerifyTOTP(context.Background(), testTenantID, userID, code); err != nil {
		t.Fatalf("VerifyTOTP: %v", err)
	}
	if err := service.VerifyTOTP(context.Background(), testTenantID, userID, code); !errors.Is(err, ErrInvalidMFA) {
		t.Fatalf("replayed TOTP error = %v, want ErrInvalidMFA", err)
	}
}

func TestVerifyTOTPDoesNotExposeAbsentDevice(t *testing.T) {
	service, store, _ := newTestMFAService(t)
	store.found = false
	if err := service.VerifyTOTP(context.Background(), testTenantID, store.device.UserID, "123456"); !errors.Is(err, ErrInvalidMFA) {
		t.Fatalf("VerifyTOTP = %v, want ErrInvalidMFA", err)
	}
}

func TestVerifyTOTPReportsSecretStoreFailureAsUnavailable(t *testing.T) {
	service, _, _ := newTestMFAService(t)
	service.secrets = memorySecrets{}
	if err := service.VerifyTOTP(context.Background(), testTenantID, "33333333-3333-4333-8333-333333333333", "123456"); !errors.Is(err, ErrMFAUnavailable) {
		t.Fatalf("VerifyTOTP = %v, want ErrMFAUnavailable", err)
	}
}
