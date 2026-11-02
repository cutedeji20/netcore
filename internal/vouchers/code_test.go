package vouchers

import (
	"strings"
	"testing"
)

func TestCodeGenerationAndHashLookup(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		code, err := NewCode()
		if err != nil {
			t.Fatal(err)
		}
		if seen[code] || len(code) != 17 {
			t.Fatalf("duplicate or incorrectly sized code %q", code)
		}
		if !strings.HasPrefix(code, "NC-") || strings.ContainsAny(code, "01ILO") {
			t.Fatalf("code is not customer-entry safe: %q", code)
		}
		seen[code] = true
		if got := HashCode(code); got != HashCode(code) {
			t.Fatal("unstable hash")
		}
	}
}

func TestLegacyCodeNormalizationRemainsCompatible(t *testing.T) {
	legacy := "NC-FPXC6Q-K5ZR7S-7XM43Q"
	if HashCode(legacy) != HashCode("ncfpxc6qk5zr7s7xm43q") {
		t.Fatal("legacy voucher formatting must remain accepted")
	}
}
