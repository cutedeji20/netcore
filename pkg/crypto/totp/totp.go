// Package totp implements the TOTP algorithm used for multi-factor
// authentication. It owns no persistence and never writes a secret to logs.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // RFC 6238's interoperable default algorithm.
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	Period        = 30 * time.Second
	DefaultDigits = 6
	secretBytes   = 20
)

var ErrInvalidSecret = errors.New("totp: invalid secret")

// GenerateSecret creates a 160-bit Base32 secret suitable for authenticator
// applications. Callers must store it in a secret manager, never in PostgreSQL
// or an application log.
func GenerateSecret() (string, error) {
	secret := make([]byte, secretBytes)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("totp: generate secret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret), nil
}

// Code returns a TOTP code for secret at the supplied time. Only the standard
// six- and eight-digit formats are supported to avoid a configuration mismatch
// with authenticator clients.
func Code(secret string, at time.Time, digits int) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	if digits != 6 && digits != 8 {
		return "", errors.New("totp: digits must be 6 or 8")
	}
	counter := uint64(at.UTC().Unix() / int64(Period/time.Second))
	return codeForCounter(key, counter, digits), nil
}

// Verify accepts a code from the current period plus a bounded number of
// neighbouring periods. It returns the matched counter so a caller can reject
// replay of a code from an already-used period.
func Verify(secret, candidate string, at time.Time, digits, allowedSkew int) (counter int64, matched bool, err error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return 0, false, err
	}
	if (digits != 6 && digits != 8) || allowedSkew < 0 || allowedSkew > 3 || !validCode(candidate, digits) {
		return 0, false, nil
	}
	current := at.UTC().Unix() / int64(Period/time.Second)
	for offset := -allowedSkew; offset <= allowedSkew; offset++ {
		candidateCounter := current + int64(offset)
		if candidateCounter < 0 {
			continue
		}
		expected := codeForCounter(key, uint64(candidateCounter), digits)
		if hmac.Equal([]byte(candidate), []byte(expected)) {
			return candidateCounter, true, nil
		}
	}
	return 0, false, nil
}

func decodeSecret(secret string) ([]byte, error) {
	secret = strings.ToUpper(strings.TrimSpace(secret))
	if secret == "" {
		return nil, ErrInvalidSecret
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(key) < secretBytes {
		return nil, ErrInvalidSecret
	}
	return key, nil
}

func codeForCounter(key []byte, counter uint64, digits int) string {
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(message[:])
	sum := mac.Sum(nil)
	offset := int(sum[len(sum)-1] & 0x0f)
	value := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	modulus := uint32(1_000_000)
	if digits == 8 {
		modulus = 100_000_000
	}
	return fmt.Sprintf("%0*d", digits, value%modulus)
}

func validCode(code string, digits int) bool {
	if len(code) != digits {
		return false
	}
	_, err := strconv.ParseUint(code, 10, 32)
	return err == nil
}
