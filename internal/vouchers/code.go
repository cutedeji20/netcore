package vouchers

import (
	"crypto/rand"
	"crypto/sha256"
	"io"
	"strings"
)

// voucherAlphabet omits characters customers commonly confuse when reading a
// printed ticket: 0/O and 1/I/L. Twelve base-31 characters provide about 59.5
// bits of entropy while remaining practical to type on a phone.
const voucherAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// NewCode returns a short bearer secret formatted for manual entry. Previously
// issued NC-XXXXXX-XXXXXX-XXXXXX codes remain valid because lookup hashes the
// normalized code rather than enforcing the new presentation format.
func NewCode() (string, error) {
	const characterCount = 12
	plain := make([]byte, 0, characterCount)
	random := make([]byte, 32)
	// Reject the top eight byte values so each of the 31 characters has the
	// same probability (248 is the largest multiple of 31 below 256).
	for len(plain) < characterCount {
		if _, err := io.ReadFull(rand.Reader, random); err != nil {
			return "", err
		}
		for _, value := range random {
			if value >= 248 {
				continue
			}
			plain = append(plain, voucherAlphabet[int(value)%len(voucherAlphabet)])
			if len(plain) == characterCount {
				break
			}
		}
	}
	return "NC-" + string(plain[:4]) + "-" + string(plain[4:8]) + "-" + string(plain[8:]), nil
}

func normalizeCode(code string) string {
	return strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(code)), "-", "")
}

func HashCode(code string) [32]byte { return sha256.Sum256([]byte(normalizeCode(code))) }
