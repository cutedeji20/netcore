package vouchers

import (
	"testing"
)

func TestCodeGenerationAndHashLookup(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		code, err := NewCode()
		if err != nil {
			t.Fatal(err)
		}
		if seen[code] || len(code) != 23 {
			t.Fatalf("duplicate or short code")
		}
		seen[code] = true
		if got := HashCode(code); got != HashCode(code) {
			t.Fatal("unstable hash")
		}
	}
}
