package auth

import (
	"context"
	"errors"
	"testing"
	"time"

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
	service, err := NewMFAService(store, memorySecrets{store.device.SecretRef: secret})
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
