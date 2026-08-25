package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/pkg/crypto/envelope"
	"github.com/netcore-isp/netcore/pkg/crypto/totp"
)

const (
	mfaAADPrefix           = "netcore/mfa/v1/"
	staffInvitationSubject = "staff-invitation"
	userTOTPMFASubject     = "user-mfa-totp"
)

var ErrInvalidMFAEnvelope = errors.New("auth: invalid MFA envelope")

// MFASecretEnvelope is the persisted, encrypted representation of a TOTP
// secret. It deliberately contains no plaintext and is safe to store only in
// the MFA persistence layer.
type MFASecretEnvelope struct {
	Ciphertext []byte
	Nonce      []byte
	WrappedDEK []byte
	KEKKeyID   string
}

// SealTOTPSecret validates and encrypts a TOTP secret for one explicitly
// scoped subject. Only invitation and enrolled-user MFA namespaces may own an
// MFA envelope.
func SealTOTPSecret(ctx context.Context, wrapper envelope.KeyWrapper, tenantID, subjectKind, subjectID, secret string) (MFASecretEnvelope, error) {
	if !validMFASubject(tenantID, subjectKind, subjectID) {
		return MFASecretEnvelope{}, ErrInvalidMFAEnvelope
	}
	if _, err := totp.Code(secret, time.Now(), totp.DefaultDigits); err != nil {
		return MFASecretEnvelope{}, ErrInvalidMFAEnvelope
	}
	record, err := envelope.Seal(ctx, wrapper, mfaAAD(tenantID, subjectKind, subjectID), []byte(secret))
	if err != nil {
		return MFASecretEnvelope{}, ErrInvalidMFAEnvelope
	}
	return MFASecretEnvelope{
		Ciphertext: record.Ciphertext,
		Nonce:      record.Nonce,
		WrappedDEK: record.WrappedDEK,
		KEKKeyID:   record.KEKKeyID,
	}, nil
}

// OpenTOTPSecret authenticates and opens an MFA secret only for its original
// tenant and subject. Callers must keep the returned value in process memory
// only long enough to verify a code.
func OpenTOTPSecret(ctx context.Context, wrapper envelope.KeyWrapper, tenantID, subjectKind, subjectID string, value MFASecretEnvelope) (string, error) {
	if !validMFASubject(tenantID, subjectKind, subjectID) || !value.complete() {
		return "", ErrInvalidMFAEnvelope
	}
	plaintext, err := envelope.Open(ctx, wrapper, mfaAAD(tenantID, subjectKind, subjectID), envelope.Record{
		Ciphertext: value.Ciphertext,
		Nonce:      value.Nonce,
		WrappedDEK: value.WrappedDEK,
		KEKKeyID:   value.KEKKeyID,
	})
	if err != nil {
		return "", ErrInvalidMFAEnvelope
	}
	secret := string(plaintext)
	if _, err := totp.Code(secret, time.Now(), totp.DefaultDigits); err != nil {
		return "", ErrInvalidMFAEnvelope
	}
	return secret, nil
}

func (value MFASecretEnvelope) complete() bool {
	return len(value.Ciphertext) > 0 && len(value.Nonce) > 0 && len(value.WrappedDEK) > 0 && strings.TrimSpace(value.KEKKeyID) != ""
}

func (value MFASecretEnvelope) present() bool {
	return len(value.Ciphertext) > 0 || len(value.Nonce) > 0 || len(value.WrappedDEK) > 0 || strings.TrimSpace(value.KEKKeyID) != ""
}

func validMFASubject(tenantID, subjectKind, subjectID string) bool {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(subjectID) == "" {
		return false
	}
	return subjectKind == staffInvitationSubject || subjectKind == userTOTPMFASubject
}

func mfaAAD(tenantID, subjectKind, subjectID string) []byte {
	return []byte(mfaAADPrefix + tenantID + "/" + subjectKind + "/" + subjectID)
}
