// Package argon2id provides password hashing using the Argon2id PHC format.
//
// It is intentionally narrow: application packages get Hash, Verify and
// NeedsRehash rather than direct access to password derivation primitives.
package argon2id

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const algorithm = "argon2id"

var ErrInvalidHash = errors.New("argon2id: invalid password hash")

// Params are the Argon2id parameters persisted in a PHC string with each hash.
// Memory is measured in KiB.
type Params struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultParams is the Phase 2 starting policy for a two-vCPU API node.
func DefaultParams() Params {
	return Params{
		Memory:      64 * 1024,
		Iterations:  3,
		Parallelism: 2,
		SaltLength:  16,
		KeyLength:   32,
	}
}

// Hasher hashes and verifies passwords using one current policy.
type Hasher struct{ params Params }

// New returns a Hasher after validating its policy.
func New(params Params) (Hasher, error) {
	if params.Memory == 0 || params.Iterations == 0 || params.Parallelism == 0 || params.SaltLength < 16 || params.KeyLength < 16 {
		return Hasher{}, fmt.Errorf("argon2id: invalid parameters")
	}
	return Hasher{params: params}, nil
}

// Params returns the current hashing policy.
func (h Hasher) Params() Params { return h.params }

// Hash returns a self-describing PHC hash. The caller must never log either
// the password or the returned hash.
func (h Hasher) Hash(password string) (string, error) {
	salt := make([]byte, h.params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("argon2id: generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, h.params.Iterations, h.params.Memory, h.params.Parallelism, h.params.KeyLength)
	return encode(h.params, salt, key), nil
}

// Verify compares password against encoded using constant-time key comparison.
// A malformed PHC string is an error, not a failed login, so operators can
// distinguish damaged credential data from a normal bad password internally.
func (h Hasher) Verify(password, encoded string) (bool, error) {
	params, salt, expected, err := decode(encoded)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

// NeedsRehash reports whether encoded is weaker than the current policy. A
// stronger stored hash is retained; lowering an existing credential's cost on
// login would be a silent security regression. Call it only after Verify
// succeeds.
func (h Hasher) NeedsRehash(encoded string) (bool, error) {
	params, _, key, err := decode(encoded)
	if err != nil {
		return false, err
	}
	return params.Memory < h.params.Memory ||
		params.Iterations < h.params.Iterations ||
		params.Parallelism < h.params.Parallelism ||
		params.SaltLength < h.params.SaltLength ||
		uint32(len(key)) < h.params.KeyLength, nil
}

func encode(params Params, salt, key []byte) string {
	return fmt.Sprintf("$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		algorithm,
		argon2.Version,
		params.Memory,
		params.Iterations,
		params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

func decode(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != algorithm {
		return Params{}, nil, nil, ErrInvalidHash
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return Params{}, nil, nil, ErrInvalidHash
	}

	params, err := parseParams(parts[3])
	if err != nil {
		return Params{}, nil, nil, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || uint32(len(salt)) < 16 {
		return Params{}, nil, nil, ErrInvalidHash
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(key) < 16 {
		return Params{}, nil, nil, ErrInvalidHash
	}
	params.SaltLength = uint32(len(salt))
	params.KeyLength = uint32(len(key))
	return params, salt, key, nil
}

func parseParams(v string) (Params, error) {
	parts := strings.Split(v, ",")
	if len(parts) != 3 {
		return Params{}, ErrInvalidHash
	}
	values := make(map[string]string, len(parts))
	for _, part := range parts {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return Params{}, ErrInvalidHash
		}
		if _, exists := values[key]; exists {
			return Params{}, ErrInvalidHash
		}
		values[key] = value
	}
	memory, err := parseUint32(values["m"])
	if err != nil || memory == 0 {
		return Params{}, ErrInvalidHash
	}
	iterations, err := parseUint32(values["t"])
	if err != nil || iterations == 0 {
		return Params{}, ErrInvalidHash
	}
	parallelism, err := parseUint8(values["p"])
	if err != nil || parallelism == 0 {
		return Params{}, ErrInvalidHash
	}
	return Params{Memory: memory, Iterations: iterations, Parallelism: parallelism}, nil
}

func parseUint32(v string) (uint32, error) {
	n, err := strconv.ParseUint(v, 10, 32)
	return uint32(n), err
}

func parseUint8(v string) (uint8, error) {
	n, err := strconv.ParseUint(v, 10, 8)
	return uint8(n), err
}
