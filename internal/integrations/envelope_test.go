package integrations

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// testKeyWrapper models the external KMS boundary without putting a real
// provider credential or a cloud dependency in an encryption-unit test.
type testKeyWrapper struct {
	keyID string
}

func (w testKeyWrapper) Wrap(_ context.Context, dek []byte) (WrappedDEK, error) {
	return WrappedDEK{Ciphertext: append([]byte(nil), dek...), KeyID: w.keyID}, nil
}

func (w testKeyWrapper) Unwrap(_ context.Context, wrapped []byte, keyID string) ([]byte, error) {
	if keyID != w.keyID {
		return nil, errors.New("test key id mismatch")
	}
	return append([]byte(nil), wrapped...), nil
}

func TestEnvelopeRoundTripBindsCredentialToTenantAndProvider(t *testing.T) {
	// This fails if a credential envelope can be replayed by another tenant or
	// provider, which would leak one customer's integration key to another.
	wrapper := testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}
	envelope, err := EncryptCredential(context.Background(), wrapper, "tenant-a", ProviderResend, []byte("re_credential_value"))
	if err != nil {
		t.Fatalf("EncryptCredential: %v", err)
	}

	got, err := DecryptCredential(context.Background(), wrapper, "tenant-a", ProviderResend, envelope)
	if err != nil {
		t.Fatalf("DecryptCredential: %v", err)
	}
	if want := "re_credential_value"; string(got) != want {
		t.Fatalf("decrypted credential = %q, want %q", got, want)
	}

	if _, err := DecryptCredential(context.Background(), wrapper, "tenant-b", ProviderResend, envelope); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("other tenant error = %v, want ErrCredentialInvalid", err)
	}
	if _, err := DecryptCredential(context.Background(), wrapper, "tenant-a", ProviderPaystack, envelope); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("other provider error = %v, want ErrCredentialInvalid", err)
	}
}

func TestEncryptCredentialUsesFreshNonceAndCiphertextOnRotation(t *testing.T) {
	// This fails if repeated rotations reuse encryption material and turn the
	// ciphertext into a deterministic identifier for the provider key.
	wrapper := testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}
	first, err := EncryptCredential(context.Background(), wrapper, "tenant-a", ProviderPaystack, []byte("sk_test_credential"))
	if err != nil {
		t.Fatalf("first EncryptCredential: %v", err)
	}
	second, err := EncryptCredential(context.Background(), wrapper, "tenant-a", ProviderPaystack, []byte("sk_test_credential"))
	if err != nil {
		t.Fatalf("second EncryptCredential: %v", err)
	}
	if bytes.Equal(first.Nonce, second.Nonce) {
		t.Fatal("credential rotations reused a nonce")
	}
	if bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("credential rotations reused ciphertext")
	}
}

func TestEncryptCredentialRejectsUnknownProviderAndMissingKeyIdentity(t *testing.T) {
	// This fails if input validation allows an unscoped AAD value or an envelope
	// that cannot be safely unwrapped after it is persisted.
	if _, err := EncryptCredential(context.Background(), testKeyWrapper{keyID: "https://vault.example/keys/integrations/1"}, "tenant-a", Provider("other"), []byte("credential")); err == nil {
		t.Fatal("unknown provider was accepted")
	}
	if _, err := EncryptCredential(context.Background(), testKeyWrapper{}, "tenant-a", ProviderResend, []byte("credential")); err == nil {
		t.Fatal("missing key identity was accepted")
	}
}

func TestUnavailableKeyWrapperFailsClosed(t *testing.T) {
	// This fails if a staged deployment without Key Vault can accidentally save
	// a provider credential through an alternate local secret path.
	if _, err := EncryptCredential(context.Background(), NewUnavailableKeyWrapper(), "tenant-a", ProviderResend, []byte("credential")); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("EncryptCredential error = %v, want ErrKeyUnavailable", err)
	}
}
