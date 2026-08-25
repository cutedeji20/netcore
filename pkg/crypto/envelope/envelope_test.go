package envelope

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// testWrapper preserves the Key Vault contract relevant to an envelope: a
// wrapped key is usable only by the immutable key version that created it.
type testWrapper struct {
	keyID string
}

func (w testWrapper) Wrap(_ context.Context, dek []byte) (WrappedKey, error) {
	return WrappedKey{Ciphertext: append([]byte(nil), dek...), KeyID: w.keyID}, nil
}

func (w testWrapper) Unwrap(_ context.Context, wrapped []byte, keyID string) ([]byte, error) {
	if keyID != w.keyID {
		return nil, errors.New("test key id mismatch")
	}
	return append([]byte(nil), wrapped...), nil
}

func TestSealAndOpenBindsPlaintextToAAD(t *testing.T) {
	// Removing AAD from GCM would let this record be replayed for another
	// invitation, leaking its protected secret.
	wrapper := testWrapper{keyID: "https://vault.example/keys/kek/version"}
	record, err := Seal(context.Background(), wrapper, []byte("tenant-a/staff-invitation/a"), []byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Open(context.Background(), wrapper, []byte("tenant-a/staff-invitation/b"), record)
	if !errors.Is(err, ErrAuthentication) {
		t.Fatalf("AAD swap error = %v, want ErrAuthentication", err)
	}
}

func TestSealFailsClosedWhenWrapperUnavailable(t *testing.T) {
	// Ignoring a wrapping failure would persist a record whose DEK is exposed or
	// permanently unrecoverable.
	_, err := Seal(context.Background(), failingWrapper{}, []byte("tenant-a/invitation/a"), []byte("secret"))
	if !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("Seal error = %v, want ErrKeyUnavailable", err)
	}
}

func TestOpenRejectsMalformedNonce(t *testing.T) {
	// Skipping nonce-length validation can pass malformed persisted data to GCM.
	wrapper := testWrapper{keyID: "https://vault.example/keys/kek/version"}
	record, err := Seal(context.Background(), wrapper, []byte("tenant-a/invitation/a"), []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	record.Nonce = record.Nonce[:len(record.Nonce)-1]
	if _, err := Open(context.Background(), wrapper, []byte("tenant-a/invitation/a"), record); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("Open error = %v, want ErrInvalidRecord", err)
	}
}

func TestOpenRejectsWrappedKeyFromAnotherVersion(t *testing.T) {
	// Passing the wrong immutable KEK version would make a record decryptable
	// through whichever key the wrapper happens to select.
	wrapper := testWrapper{keyID: "https://vault.example/keys/kek/version-a"}
	record, err := Seal(context.Background(), wrapper, []byte("tenant-a/invitation/a"), []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	record.KEKKeyID = "https://vault.example/keys/kek/version-b"
	if _, err := Open(context.Background(), wrapper, []byte("tenant-a/invitation/a"), record); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("Open error = %v, want ErrKeyUnavailable", err)
	}
}

func TestSealUsesFreshNonceAndCiphertext(t *testing.T) {
	// Reusing a DEK or nonce turns equal secrets into a deterministic value and
	// can compromise AES-GCM confidentiality.
	wrapper := testWrapper{keyID: "https://vault.example/keys/kek/version"}
	first, err := Seal(context.Background(), wrapper, []byte("tenant-a/invitation/a"), []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Seal(context.Background(), wrapper, []byte("tenant-a/invitation/a"), []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Nonce, second.Nonce) {
		t.Fatal("seals reused a nonce")
	}
	if bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("seals reused ciphertext")
	}
}

type failingWrapper struct{}

func (failingWrapper) Wrap(context.Context, []byte) (WrappedKey, error) {
	return WrappedKey{}, errors.New("key vault unavailable")
}

func (failingWrapper) Unwrap(context.Context, []byte, string) ([]byte, error) {
	return nil, errors.New("key vault unavailable")
}
