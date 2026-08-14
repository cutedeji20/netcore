package argon2id

import (
	"errors"
	"testing"
)

func testHasher(t *testing.T) Hasher {
	t.Helper()
	h, err := New(Params{Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h
}

func TestHashAndVerify(t *testing.T) {
	h := testHasher(t)
	encoded, err := h.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if got, want := encoded[:10], "$argon2id$"; got != want {
		t.Fatalf("hash prefix = %q, want %q", got, want)
	}

	matched, err := h.Verify("correct horse battery staple", encoded)
	if err != nil || !matched {
		t.Fatalf("correct password matched=%v err=%v", matched, err)
	}
	matched, err = h.Verify("wrong", encoded)
	if err != nil || matched {
		t.Fatalf("wrong password matched=%v err=%v", matched, err)
	}
}

func TestNeedsRehash(t *testing.T) {
	weak := testHasher(t)
	encoded, err := weak.Hash("password")
	if err != nil {
		t.Fatal(err)
	}
	strong, err := New(Params{Memory: 16 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatal(err)
	}
	needs, err := strong.NeedsRehash(encoded)
	if err != nil || !needs {
		t.Fatalf("NeedsRehash=%v err=%v, want true nil", needs, err)
	}
	strongHash, err := strong.Hash("password")
	if err != nil {
		t.Fatal(err)
	}
	needs, err = weak.NeedsRehash(strongHash)
	if err != nil || needs {
		t.Fatalf("weaker policy must not downgrade a stronger hash: needs=%v err=%v", needs, err)
	}
}

func TestMalformedHashIsRejected(t *testing.T) {
	h := testHasher(t)
	for _, encoded := range []string{
		"$argon2id$v=19$m=1,t=1,p=1$bad$bad",
		"$argon2id$v=19$m=8192,m=8192,p=1$YWJjZGVmZ2hpamtsbW5vcA$YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY",
	} {
		if _, err := h.Verify("password", encoded); !errors.Is(err, ErrInvalidHash) {
			t.Fatalf("Verify(%q) error = %v, want ErrInvalidHash", encoded, err)
		}
	}
}
