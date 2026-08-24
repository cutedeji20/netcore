package integrations

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	dataEncryptionKeyLength = 32
	credentialMaxLength     = 16 * 1024
	credentialAADPrefix     = "netcore/integration/v1/"
)

// CredentialEnvelope is the database-safe representation of a provider
// credential. It intentionally has no plaintext field.
type CredentialEnvelope struct {
	Ciphertext []byte
	Nonce      []byte
	WrappedDEK []byte
	KEKKeyID   string
}

// EncryptCredential protects one provider credential with a fresh random DEK
// and binds the encrypted value to exactly one tenant/provider pair.
func EncryptCredential(ctx context.Context, wrapper KeyWrapper, tenantID string, provider Provider, credential []byte) (CredentialEnvelope, error) {
	if wrapper == nil {
		return CredentialEnvelope{}, ErrKeyUnavailable
	}
	if !provider.Valid() {
		return CredentialEnvelope{}, ErrInvalidProvider
	}
	if !validTenantID(tenantID) {
		return CredentialEnvelope{}, ErrInvalidTenant
	}
	if len(credential) == 0 || len(credential) > credentialMaxLength {
		return CredentialEnvelope{}, ErrInvalidCredential
	}

	dek := make([]byte, dataEncryptionKeyLength)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return CredentialEnvelope{}, fmt.Errorf("%w: generate data encryption key", ErrKeyUnavailable)
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return CredentialEnvelope{}, fmt.Errorf("%w: create cipher", ErrKeyUnavailable)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return CredentialEnvelope{}, fmt.Errorf("%w: create authenticated cipher", ErrKeyUnavailable)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return CredentialEnvelope{}, fmt.Errorf("%w: generate nonce", ErrKeyUnavailable)
	}
	ciphertext := gcm.Seal(nil, nonce, credential, credentialAAD(tenantID, provider))
	wrapped, err := wrapper.Wrap(ctx, dek)
	if err != nil || len(wrapped.Ciphertext) == 0 || strings.TrimSpace(wrapped.KeyID) == "" {
		return CredentialEnvelope{}, ErrKeyUnavailable
	}

	return CredentialEnvelope{
		Ciphertext: append([]byte(nil), ciphertext...),
		Nonce:      append([]byte(nil), nonce...),
		WrappedDEK: append([]byte(nil), wrapped.Ciphertext...),
		KEKKeyID:   strings.TrimSpace(wrapped.KeyID),
	}, nil
}

// DecryptCredential returns the credential only after the envelope's AAD and
// GCM authentication prove it belongs to the supplied tenant/provider pair.
func DecryptCredential(ctx context.Context, wrapper KeyWrapper, tenantID string, provider Provider, envelope CredentialEnvelope) ([]byte, error) {
	if wrapper == nil {
		return nil, ErrKeyUnavailable
	}
	if !provider.Valid() {
		return nil, ErrInvalidProvider
	}
	if !validTenantID(tenantID) {
		return nil, ErrInvalidTenant
	}
	if len(envelope.Ciphertext) == 0 || len(envelope.WrappedDEK) == 0 || strings.TrimSpace(envelope.KEKKeyID) == "" {
		return nil, ErrInvalidEnvelope
	}

	dek, err := wrapper.Unwrap(ctx, envelope.WrappedDEK, envelope.KEKKeyID)
	if err != nil || len(dek) != dataEncryptionKeyLength {
		return nil, ErrKeyUnavailable
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, ErrKeyUnavailable
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrKeyUnavailable
	}
	if len(envelope.Nonce) != gcm.NonceSize() {
		return nil, ErrInvalidEnvelope
	}
	credential, err := gcm.Open(nil, envelope.Nonce, envelope.Ciphertext, credentialAAD(tenantID, provider))
	if err != nil {
		return nil, ErrCredentialInvalid
	}
	if len(credential) == 0 || len(credential) > credentialMaxLength {
		return nil, errors.New("integrations: decrypted credential violates policy")
	}
	return credential, nil
}

func credentialAAD(tenantID string, provider Provider) []byte {
	return []byte(credentialAADPrefix + strings.TrimSpace(tenantID) + "/" + string(provider))
}
