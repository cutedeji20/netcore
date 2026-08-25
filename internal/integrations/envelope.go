package integrations

import (
	"context"
	"errors"
	"strings"

	cryptoenvelope "github.com/netcore-isp/netcore/pkg/crypto/envelope"
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

	record, err := cryptoenvelope.Seal(ctx, wrapper, credentialAAD(tenantID, provider), credential)
	if err != nil {
		return CredentialEnvelope{}, ErrKeyUnavailable
	}

	return CredentialEnvelope{
		Ciphertext: record.Ciphertext,
		Nonce:      record.Nonce,
		WrappedDEK: record.WrappedDEK,
		KEKKeyID:   record.KEKKeyID,
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
	credential, err := cryptoenvelope.Open(ctx, wrapper, credentialAAD(tenantID, provider), cryptoenvelope.Record{
		Ciphertext: envelope.Ciphertext, Nonce: envelope.Nonce,
		WrappedDEK: envelope.WrappedDEK, KEKKeyID: envelope.KEKKeyID,
	})
	if err != nil {
		switch {
		case errors.Is(err, cryptoenvelope.ErrInvalidRecord):
			return nil, ErrInvalidEnvelope
		case errors.Is(err, cryptoenvelope.ErrAuthentication):
			return nil, ErrCredentialInvalid
		default:
			return nil, ErrKeyUnavailable
		}
	}
	if len(credential) == 0 || len(credential) > credentialMaxLength {
		return nil, errors.New("integrations: decrypted credential violates policy")
	}
	return credential, nil
}

func credentialAAD(tenantID string, provider Provider) []byte {
	return []byte(credentialAADPrefix + strings.TrimSpace(tenantID) + "/" + string(provider))
}
