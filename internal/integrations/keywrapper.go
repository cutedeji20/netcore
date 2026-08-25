package integrations

import (
	"context"

	"github.com/netcore-isp/netcore/pkg/crypto/envelope"
)

// WrappedDEK is opaque ciphertext returned by the configured key-encryption
// service. KeyID identifies the immutable key version that performed wrapping.
type WrappedDEK = envelope.WrappedKey

// KeyWrapper is the small boundary between the data-encryption envelope and a
// hardware-backed or cloud key-encryption key. Implementations must not retain
// the supplied DEK after Wrap or Unwrap returns.
type KeyWrapper = envelope.KeyWrapper

// NewUnavailableKeyWrapper supports a staged deployment before the Azure Key
// Vault KEK is provisioned. It is intentionally usable only for safe metadata
// reads; every cryptographic operation fails closed.
func NewUnavailableKeyWrapper() KeyWrapper { return unavailableKeyWrapper{} }

type unavailableKeyWrapper struct{}

func (unavailableKeyWrapper) Wrap(context.Context, []byte) (WrappedDEK, error) {
	return WrappedDEK{}, ErrKeyUnavailable
}

func (unavailableKeyWrapper) Unwrap(context.Context, []byte, string) ([]byte, error) {
	return nil, ErrKeyUnavailable
}
