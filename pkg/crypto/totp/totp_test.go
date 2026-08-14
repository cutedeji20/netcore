package totp

import (
	"testing"
	"time"
)

const rfc6238Secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestCodeMatchesRFC6238SHA1Vector(t *testing.T) {
	code, err := Code(rfc6238Secret, time.Unix(59, 0), 8)
	if err != nil {
		t.Fatal(err)
	}
	if code != "94287082" {
		t.Fatalf("code = %q, want 94287082", code)
	}
}

func TestVerifyReturnsMatchedCounterWithinSkew(t *testing.T) {
	at := time.Unix(1111111111, 0)
	code, err := Code(rfc6238Secret, at.Add(-Period), DefaultDigits)
	if err != nil {
		t.Fatal(err)
	}
	counter, matched, err := Verify(rfc6238Secret, code, at, DefaultDigits, 1)
	if err != nil || !matched {
		t.Fatalf("Verify = %d, %t, %v", counter, matched, err)
	}
	if want := at.Add(-Period).Unix() / int64(Period/time.Second); counter != want {
		t.Fatalf("counter = %d, want %d", counter, want)
	}
}

func TestVerifyRejectsInvalidSecretsAndCodes(t *testing.T) {
	if _, _, err := Verify("not a secret", "123456", time.Now(), DefaultDigits, 1); err == nil {
		t.Fatal("malformed secret was accepted")
	}
	if _, matched, err := Verify(rfc6238Secret, "abc123", time.Now(), DefaultDigits, 1); err != nil || matched {
		t.Fatalf("malformed code = matched %t, err %v", matched, err)
	}
}
