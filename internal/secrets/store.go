// Package secrets provides the deployment boundary for secret values. Domain
// packages receive only the small Resolver interface they need; neither a
// secret backend nor a plaintext value belongs in PostgreSQL.
package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxSecretDocumentBytes = 64 * 1024

var (
	ErrNotFound        = errors.New("secrets: reference not found")
	ErrInvalidRef      = errors.New("secrets: invalid reference")
	ErrUnsupported     = errors.New("secrets: backend is not available")
	ErrInvalidDocument = errors.New("secrets: invalid secret document")
)

// Resolver is the intentionally narrow SecretStore seam. Its callers retain
// a logical reference such as "payments.paystack.secret_key", never a secret
// value or a backend-specific path in a business record.
type Resolver interface {
	Resolve(context.Context, string) (string, error)
}

// SOPSFileStore reads a JSON document that the deployment process has already
// decrypted from SOPS into a read-only tmpfs mount. The source repository and
// image therefore hold only ciphertext; this process never receives a secret
// through application configuration or an environment variable.
//
// Vault is intentionally not emulated here. Selecting it before its access
// policy and audit trail are deployed fails closed rather than silently using
// a weaker source.
type SOPSFileStore struct {
	path string
}

func NewSOPSFileStore(path string) (*SOPSFileStore, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: SOPS document path must be absolute", ErrInvalidRef)
	}
	return &SOPSFileStore{path: filepath.Clean(path)}, nil
}

// Resolve reads a flat JSON object from the mount on each request. It avoids a
// long-lived in-process copy and makes an atomically replaced secret document
// visible without a process restart. The document must be small and contain
// only non-empty string values, for example:
// {"payments.paystack.secret_key":"..."}.
func (s *SOPSFileStore) Resolve(ctx context.Context, reference string) (string, error) {
	if s == nil || s.path == "" {
		return "", ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !validReference(reference) {
		return "", ErrInvalidRef
	}

	file, err := os.Open(s.path)
	if err != nil {
		return "", fmt.Errorf("secrets: open mounted document: %w", err)
	}
	defer file.Close()

	limited := io.LimitReader(file, maxSecretDocumentBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("secrets: read mounted document: %w", err)
	}
	if len(body) == 0 || len(body) > maxSecretDocumentBytes {
		return "", ErrInvalidDocument
	}

	var values map[string]string
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&values); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidDocument, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", ErrInvalidDocument
	}
	value, ok := values[reference]
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		return "", ErrNotFound
	}
	return value, nil
}

func validReference(reference string) bool {
	if len(reference) < 3 || len(reference) > 160 {
		return false
	}
	for _, char := range reference {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}
