package vouchers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"io"
	"strings"
)

var codeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewCode returns an 88-bit bearer secret formatted for manual entry.
func NewCode() (string, error) {
	raw := make([]byte, 11)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	encoded := codeEncoding.EncodeToString(raw)
	return "NC-" + encoded[:6] + "-" + encoded[6:12] + "-" + encoded[12:], nil
}

func normalizeCode(code string) string {
	return strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(code)), "-", "")
}

func HashCode(code string) [32]byte { return sha256.Sum256([]byte(normalizeCode(code))) }
