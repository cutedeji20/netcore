package network

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/netcore-isp/netcore/internal/integrations"
)

type credentialTestWrapper struct{ keyID string }

func (w credentialTestWrapper) Wrap(_ context.Context, dek []byte) (integrations.WrappedDEK, error) {
	return integrations.WrappedDEK{Ciphertext: append([]byte(nil), dek...), KeyID: w.keyID}, nil
}

func (w credentialTestWrapper) Unwrap(_ context.Context, wrapped []byte, keyID string) ([]byte, error) {
	if keyID != w.keyID {
		return nil, errors.New("unexpected key identity")
	}
	return append([]byte(nil), wrapped...), nil
}

func TestGenerateSharedSecretUsesFreshURLSafeValues(t *testing.T) {
	first, err := GenerateSharedSecret()
	if err != nil {
		t.Fatalf("GenerateSharedSecret first: %v", err)
	}
	second, err := GenerateSharedSecret()
	if err != nil {
		t.Fatalf("GenerateSharedSecret second: %v", err)
	}
	if len(first) < 32 || bytes.Equal(first, second) {
		t.Fatalf("generated secret is short or reused: first=%d bytes equal=%t", len(first), bytes.Equal(first, second))
	}
	if strings.ContainsAny(string(first), "+/=") {
		t.Fatalf("secret is not URL-safe: %q", first)
	}
}

func TestNASSecretEnvelopeBindsTenantNASAndVersion(t *testing.T) {
	wrapper := credentialTestWrapper{keyID: "https://vault.example/keys/netcore-provider-kek/version"}
	tenantID := "11111111-1111-4111-8111-111111111111"
	nasID := "22222222-2222-4222-8222-222222222222"
	secret := []byte("unique-radius-shared-secret")

	envelope, err := EncryptNASSecret(context.Background(), wrapper, tenantID, nasID, 1, secret)
	if err != nil {
		t.Fatalf("EncryptNASSecret: %v", err)
	}
	if bytes.Contains(envelope.Ciphertext, secret) || bytes.Equal(envelope.WrappedDEK, secret) {
		t.Fatal("encrypted envelope retained plaintext secret")
	}

	got, err := DecryptNASSecret(context.Background(), wrapper, tenantID, nasID, 1, envelope)
	if err != nil {
		t.Fatalf("DecryptNASSecret: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatalf("decrypted secret = %q, want %q", got, secret)
	}

	for _, input := range []struct {
		name     string
		tenantID string
		nasID    string
		version  int64
	}{
		{name: "tenant", tenantID: "33333333-3333-4333-8333-333333333333", nasID: nasID, version: 1},
		{name: "nas", tenantID: tenantID, nasID: "44444444-4444-4444-8444-444444444444", version: 1},
		{name: "version", tenantID: tenantID, nasID: nasID, version: 2},
	} {
		t.Run(input.name, func(t *testing.T) {
			if _, err := DecryptNASSecret(context.Background(), wrapper, input.tenantID, input.nasID, input.version, envelope); !errors.Is(err, ErrCredentialInvalid) {
				t.Fatalf("DecryptNASSecret error = %v, want ErrCredentialInvalid", err)
			}
		})
	}
}
