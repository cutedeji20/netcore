package secrets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSOPSFileStoreResolvesLogicalReference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netcore.json")
	if err := os.WriteFile(path, []byte(`{"payments.paystack.secret_key":"sk_test_value"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewSOPSFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Resolve(context.Background(), "payments.paystack.secret_key")
	if err != nil || value != "sk_test_value" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	if _, err := store.Resolve(context.Background(), "payments.missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing reference error=%v", err)
	}
}

func TestSOPSFileStoreRejectsUnsafeSourcesAndReferences(t *testing.T) {
	if _, err := NewSOPSFileStore("relative.json"); !errors.Is(err, ErrInvalidRef) {
		t.Fatalf("relative path error=%v", err)
	}
	path := filepath.Join(t.TempDir(), "netcore.json")
	if err := os.WriteFile(path, []byte(`{"safe":"value"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewSOPSFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(context.Background(), "../../etc/passwd"); !errors.Is(err, ErrInvalidRef) {
		t.Fatalf("unsafe reference error=%v", err)
	}
}
