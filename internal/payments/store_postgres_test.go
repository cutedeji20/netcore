package payments

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPaymentIdempotencyReferenceHasExplicitSQLType(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "store_postgres.go"))
	if err != nil {
		t.Fatalf("read payment store: %v", err)
	}
	if !strings.Contains(string(body), "jsonb_build_object('provider_reference', $5::text)") {
		t.Fatal("payment idempotency reference must be explicitly typed for PostgreSQL")
	}
}

func TestPaymentActivationOutboxValuesHaveExplicitSQLTypes(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "store_postgres.go"))
	if err != nil {
		t.Fatalf("read payment store: %v", err)
	}
	for _, fragment := range []string{
		"'period_start', $6::timestamptz",
		"'period_end', $7::timestamptz",
		"'quota_bytes', $8::bigint",
		"'verified_at', $10::timestamptz",
		"'starts_at', $9::timestamptz",
		"'expires_at', $10::timestamptz",
	} {
		if !strings.Contains(string(body), fragment) {
			t.Fatalf("payment outbox value must be explicitly typed: %s", fragment)
		}
	}
}
