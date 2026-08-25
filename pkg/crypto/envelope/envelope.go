// Package envelope provides authenticated envelope encryption backed by an
// external key-encryption key such as Azure Key Vault.
package envelope

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
	"strings"
)

const dataEncryptionKeyLength = 32

var (
	// ErrInvalidAAD indicates that a record was not supplied with a binding
	// context. Every record must have non-empty additional authenticated data.
	ErrInvalidAAD = errors.New("envelope: additional authenticated data is required")
	// ErrInvalidRecord indicates malformed persisted envelope data.
	ErrInvalidRecord = errors.New("envelope: invalid record")
	// ErrKeyUnavailable indicates that the external key-encryption service
	// could not safely wrap or unwrap a data-encryption key.
	ErrKeyUnavailable = errors.New("envelope: key unavailable")
	// ErrAuthentication indicates that AES-GCM could not authenticate the
	// ciphertext for the supplied record and AAD.
	ErrAuthentication = errors.New("envelope: authentication failed")
)

// WrappedKey is opaque ciphertext returned by the configured key-encryption
// service. KeyID identifies the immutable key version that performed wrapping.
type WrappedKey struct {
	Ciphertext []byte
	KeyID      string
}

// KeyWrapper is the boundary between envelope encryption and a hardware-backed
// or cloud key-encryption key.
type KeyWrapper interface {
	Wrap(context.Context, []byte) (WrappedKey, error)
	Unwrap(context.Context, []byte, string) ([]byte, error)
}

// Record is a database-safe envelope. It deliberately contains no plaintext.
type Record struct {
	Ciphertext []byte
	Nonce      []byte
	WrappedDEK []byte
	KEKKeyID   string
}

// Seal encrypts plaintext with a fresh AES-256-GCM data-encryption key and
// binds it to aad. The data-encryption key is wrapped by wrapper.
func Seal(ctx context.Context, wrapper KeyWrapper, aad, plaintext []byte) (Record, error) {
	if wrapper == nil {
		return Record{}, ErrKeyUnavailable
	}
	if len(aad) == 0 {
		return Record{}, ErrInvalidAAD
	}

	dek := make([]byte, dataEncryptionKeyLength)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return Record{}, ErrKeyUnavailable
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return Record{}, ErrKeyUnavailable
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Record{}, ErrKeyUnavailable
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Record{}, ErrKeyUnavailable
	}
	ciphertext := gcm.Seal(nil, nonce, append([]byte(nil), plaintext...), append([]byte(nil), aad...))
	wrapped, err := wrapper.Wrap(ctx, append([]byte(nil), dek...))
	if err != nil || len(wrapped.Ciphertext) == 0 || strings.TrimSpace(wrapped.KeyID) == "" {
		return Record{}, ErrKeyUnavailable
	}

	return Record{
		Ciphertext: append([]byte(nil), ciphertext...),
		Nonce:      append([]byte(nil), nonce...),
		WrappedDEK: append([]byte(nil), wrapped.Ciphertext...),
		KEKKeyID:   strings.TrimSpace(wrapped.KeyID),
	}, nil
}

// Open authenticates and decrypts record using the supplied aad.
func Open(ctx context.Context, wrapper KeyWrapper, aad []byte, record Record) ([]byte, error) {
	if wrapper == nil {
		return nil, ErrKeyUnavailable
	}
	if len(aad) == 0 {
		return nil, ErrInvalidAAD
	}
	if len(record.Ciphertext) == 0 || len(record.Nonce) == 0 || len(record.WrappedDEK) == 0 || strings.TrimSpace(record.KEKKeyID) == "" {
		return nil, ErrInvalidRecord
	}

	dek, err := wrapper.Unwrap(ctx, append([]byte(nil), record.WrappedDEK...), strings.TrimSpace(record.KEKKeyID))
	if err != nil || len(dek) != dataEncryptionKeyLength {
		return nil, ErrKeyUnavailable
	}
	block, err := aes.NewCipher(append([]byte(nil), dek...))
	if err != nil {
		return nil, ErrKeyUnavailable
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrKeyUnavailable
	}
	if len(record.Nonce) != gcm.NonceSize() {
		return nil, ErrInvalidRecord
	}
	plaintext, err := gcm.Open(nil, append([]byte(nil), record.Nonce...), append([]byte(nil), record.Ciphertext...), append([]byte(nil), aad...))
	if err != nil {
		return nil, ErrAuthentication
	}
	return append([]byte(nil), plaintext...), nil
}
