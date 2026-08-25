package auth

import (
	"context"
	"errors"
	"testing"
)

func TestTOTPSecretEnvelopeBindsTenantAndSubject(t *testing.T) {
	// This fails if a sealed MFA secret can be opened for any tenant, kind, or
	// subject other than the one which enrolled it.
	value, err := SealTOTPSecret(context.Background(), testWrapper{}, "tenant-a", "user-mfa-totp", "user-a", "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("SealTOTPSecret: %v", err)
	}
	got, err := OpenTOTPSecret(context.Background(), testWrapper{}, "tenant-a", "user-mfa-totp", "user-a", value)
	if err != nil {
		t.Fatalf("OpenTOTPSecret: %v", err)
	}
	if got != "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP" {
		t.Fatal("opened a different TOTP secret")
	}
	for _, target := range []struct{ tenant, kind, subject string }{
		{tenant: "tenant-b", kind: "user-mfa-totp", subject: "user-a"},
		{tenant: "tenant-a", kind: "staff-invitation", subject: "user-a"},
		{tenant: "tenant-a", kind: "user-mfa-totp", subject: "user-b"},
	} {
		if _, err := OpenTOTPSecret(context.Background(), testWrapper{}, target.tenant, target.kind, target.subject, value); !errors.Is(err, ErrInvalidMFAEnvelope) {
			t.Fatalf("OpenTOTPSecret(%q, %q, %q) error = %v, want ErrInvalidMFAEnvelope", target.tenant, target.kind, target.subject, err)
		}
	}
}

func TestTOTPSecretEnvelopeRejectsUnsupportedSubjectKind(t *testing.T) {
	// This fails if an unrecognised subject namespace can be used to create
	// MFA ciphertext that future code might bind inconsistently.
	if _, err := SealTOTPSecret(context.Background(), testWrapper{}, "tenant-a", "other", "user-a", "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"); !errors.Is(err, ErrInvalidMFAEnvelope) {
		t.Fatalf("SealTOTPSecret error = %v, want ErrInvalidMFAEnvelope", err)
	}
}
