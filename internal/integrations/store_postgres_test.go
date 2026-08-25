package integrations

import (
	"reflect"
	"testing"
	"time"
)

func TestIntegrationSaveArgsKeepTimestampAndResultColumnsAligned(t *testing.T) {
	// This fails if the INSERT arguments move out of alignment with
	// activated_at, last_tested_at, last_test_succeeded, updated_at, and
	// updated_by. PostgreSQL otherwise receives a boolean for a timestamp.
	activatedAt := time.Date(2026, 8, 25, 14, 45, 0, 0, time.UTC)
	testedAt := activatedAt.Add(-time.Minute)
	updatedAt := activatedAt.Add(time.Minute)
	record := Record{
		TenantID:          "tenant-a",
		Provider:          ProviderResend,
		Envelope:          CredentialEnvelope{Ciphertext: []byte("ciphertext"), Nonce: make([]byte, 12), WrappedDEK: []byte("wrapped"), KEKKeyID: "https://vault.example/keys/integrations/1"},
		SenderEmail:       "NetCore <hotspot@notify.example.test>",
		ActivatedAt:       activatedAt,
		LastTestedAt:      testedAt,
		LastTestSucceeded: true,
		UpdatedAt:         updatedAt,
		UpdatedBy:         "5d55a85d-ce4c-49bd-b5fb-8c8908d8f95f",
	}

	got := integrationSaveArgs(record)
	want := []any{activatedAt, testedAt, true, updatedAt, record.UpdatedBy}
	if !reflect.DeepEqual(got[8:], want) {
		t.Fatalf("save arguments $9-$13 = %#v, want %#v", got[8:], want)
	}
}
