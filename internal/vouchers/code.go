package vouchers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"io"
	"strings"
)

var codeEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewCode returns a 160-bit bearer secret formatted for manual entry.
func NewCode() (string, error) {
	raw := make([]byte, 20)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	encoded := codeEncoding.EncodeToString(raw)
	return "NC-" + encoded[:8] + "-" + encoded[8:16] + "-" + encoded[16:24] + "-" + encoded[24:], nil
}

func normalizeCode(code string) string {
	return strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(code)), "-", "")
}

func HashCode(code string) [32]byte { return sha256.Sum256([]byte(normalizeCode(code))) }
