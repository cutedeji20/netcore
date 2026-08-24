package integrations

import (
	"context"
	"errors"
	"testing"
)

type fakeAzureKeyOperations struct {
	wrapAlgorithm   string
	unwrapAlgorithm string
	keyID           string
	err             error
}

func (o *fakeAzureKeyOperations) WrapKey(_ context.Context, keyID, algorithm string, value []byte) ([]byte, error) {
	o.keyID = keyID
	o.wrapAlgorithm = algorithm
	if o.err != nil {
		return nil, o.err
	}
	return append([]byte("wrapped:"), value...), nil
}

func (o *fakeAzureKeyOperations) UnwrapKey(_ context.Context, keyID, algorithm string, value []byte) ([]byte, error) {
	o.keyID = keyID
	o.unwrapAlgorithm = algorithm
	if o.err != nil {
		return nil, o.err
	}
	return append([]byte(nil), value[len("wrapped:"):]...), nil
}

func TestAzureKeyVaultWrapperUsesRSAOAEP256ForEnvelopeKeys(t *testing.T) {
	// This fails if a Key Vault implementation accidentally uses a weaker or
	// incompatible wrapping algorithm for database provider credentials.
	operations := &fakeAzureKeyOperations{}
	wrapper, err := NewAzureKeyVaultWrapper(operations, "https://netcore-integrations.vault.azure.net/keys/netcore-integrations-kek/version")
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := wrapper.Wrap(context.Background(), []byte("12345678901234567890123456789012"))
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if operations.wrapAlgorithm != "RSA-OAEP-256" || wrapped.KeyID != operations.keyID {
		t.Fatalf("wrap algorithm=%q key id=%q", operations.wrapAlgorithm, wrapped.KeyID)
	}
	plain, err := wrapper.Unwrap(context.Background(), wrapped.Ciphertext, wrapped.KeyID)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if operations.unwrapAlgorithm != "RSA-OAEP-256" || string(plain) != "12345678901234567890123456789012" {
		t.Fatalf("unwrap algorithm=%q plaintext=%q", operations.unwrapAlgorithm, plain)
	}
}

func TestAzureKeyVaultWrapperSanitisesOperationFailures(t *testing.T) {
	// This fails if a Key Vault error is reflected through the integration
	// boundary where it could reveal cloud-account implementation detail.
	operations := &fakeAzureKeyOperations{err: errors.New("provider failure with sensitive details")}
	wrapper, err := NewAzureKeyVaultWrapper(operations, "https://netcore-integrations.vault.azure.net/keys/netcore-integrations-kek/version")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrapper.Wrap(context.Background(), []byte("data encryption key")); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("Wrap error=%v, want ErrKeyUnavailable", err)
	}
}
