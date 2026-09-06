package network

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/netcore-isp/netcore/internal/integrations"
	cryptoenvelope "github.com/netcore-isp/netcore/pkg/crypto/envelope"
)

const (
	radiusSecretBytes     = 32
	radiusSecretMaxLength = 512
	radiusSecretAADPrefix = "netcore/network/nas/v1/"
)

var (
	ErrInvalidNASID       = errors.New("network: invalid NAS identifier")
	ErrInvalidTenant      = errors.New("network: invalid tenant identifier")
	ErrInvalidRouterInput = errors.New("network: invalid router input")
	ErrInvalidSecret      = errors.New("network: invalid RADIUS secret")
	ErrInvalidEnvelope    = errors.New("network: invalid RADIUS secret envelope")
	ErrCredentialInvalid  = errors.New("network: RADIUS secret could not be decrypted")
	ErrKeyUnavailable     = errors.New("network: encryption key unavailable")
)

// RadiusCredentialEnvelope is the database-safe representation of a RADIUS
// shared secret. It deliberately contains no plaintext field.
type RadiusCredentialEnvelope struct {
	Ciphertext []byte
	Nonce      []byte
	WrappedDEK []byte
	KEKKeyID   string
}

// GenerateSharedSecret returns a fresh URL-safe RADIUS shared secret.
func GenerateSharedSecret() ([]byte, error) {
	raw := make([]byte, radiusSecretBytes)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return nil, ErrKeyUnavailable
	}
	return []byte(base64.RawURLEncoding.EncodeToString(raw)), nil
}

// EncryptNASSecret binds a secret to exactly one tenant, NAS, and rotation
// version so persisted encrypted material cannot be replayed for another NAS.
func EncryptNASSecret(ctx context.Context, wrapper integrations.KeyWrapper, tenantID, nasID string, version int64, secret []byte) (RadiusCredentialEnvelope, error) {
	if wrapper == nil {
		return RadiusCredentialEnvelope{}, ErrKeyUnavailable
	}
	if !validNetworkID(tenantID) {
		return RadiusCredentialEnvelope{}, ErrInvalidTenant
	}
	if !validNetworkID(nasID) {
		return RadiusCredentialEnvelope{}, ErrInvalidNASID
	}
	if version < 1 {
		return RadiusCredentialEnvelope{}, ErrInvalidEnvelope
	}
	if len(secret) == 0 || len(secret) > radiusSecretMaxLength {
		return RadiusCredentialEnvelope{}, ErrInvalidSecret
	}

	record, err := cryptoenvelope.Seal(ctx, wrapper, nasSecretAAD(tenantID, nasID, version), secret)
	if err != nil {
		return RadiusCredentialEnvelope{}, ErrKeyUnavailable
	}
	return RadiusCredentialEnvelope{
		Ciphertext: record.Ciphertext,
		Nonce:      record.Nonce,
		WrappedDEK: record.WrappedDEK,
		KEKKeyID:   record.KEKKeyID,
	}, nil
}

// DecryptNASSecret authenticates and decrypts a stored envelope only for its
// original tenant, NAS, and rotation version.
func DecryptNASSecret(ctx context.Context, wrapper integrations.KeyWrapper, tenantID, nasID string, version int64, envelope RadiusCredentialEnvelope) ([]byte, error) {
	if wrapper == nil {
		return nil, ErrKeyUnavailable
	}
	if !validNetworkID(tenantID) {
		return nil, ErrInvalidTenant
	}
	if !validNetworkID(nasID) {
		return nil, ErrInvalidNASID
	}
	if version < 1 {
		return nil, ErrInvalidEnvelope
	}

	secret, err := cryptoenvelope.Open(ctx, wrapper, nasSecretAAD(tenantID, nasID, version), cryptoenvelope.Record{
		Ciphertext: envelope.Ciphertext,
		Nonce:      envelope.Nonce,
		WrappedDEK: envelope.WrappedDEK,
		KEKKeyID:   envelope.KEKKeyID,
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
	if len(secret) == 0 || len(secret) > radiusSecretMaxLength {
		return nil, ErrInvalidSecret
	}
	return secret, nil
}

func nasSecretAAD(tenantID, nasID string, version int64) []byte {
	return []byte(radiusSecretAADPrefix + strings.TrimSpace(tenantID) + "/" + strings.TrimSpace(nasID) + "/" + strconv.FormatInt(version, 10))
}

func validNetworkID(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}
