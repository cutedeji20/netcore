package integrations

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/netcore-isp/netcore/internal/database"
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

func TestPostgresStoreSaveClassifiesMalformedRecordBeforeDatabaseAccess(t *testing.T) {
	// This fails if any incomplete persistence record is reported as a database
	// outage, which would send production investigation to the wrong layer.
	valid := Record{
		TenantID:  "tenant-a",
		Provider:  ProviderResend,
		Status:    StatusActive,
		UpdatedBy: "staff-a",
		Envelope: CredentialEnvelope{
			Ciphertext: []byte("ciphertext"),
			Nonce:      make([]byte, 12),
			WrappedDEK: []byte("wrapped"),
			KEKKeyID:   "https://vault.example/keys/integrations/1",
		},
	}

	tests := []struct {
		name   string
		mutate func(*Record)
	}{
		{name: "tenant", mutate: func(record *Record) { record.TenantID = "" }},
		{name: "provider", mutate: func(record *Record) { record.Provider = Provider("unknown") }},
		{name: "status", mutate: func(record *Record) { record.Status = StatusDisabled }},
		{name: "actor", mutate: func(record *Record) { record.UpdatedBy = "" }},
		{name: "envelope", mutate: func(record *Record) { record.Envelope = CredentialEnvelope{} }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := valid
			test.mutate(&record)

			err := (&PostgresStore{db: &database.Pool{}}).Save(context.Background(), record)
			if !errors.Is(err, ErrStorePrecondition) {
				t.Fatalf("Save error = %v, want ErrStorePrecondition", err)
			}
		})
	}
}

func TestWriteAuditEmitsTypedProviderParameter(t *testing.T) {
	// This is a query-boundary test. PostgreSQL cannot infer an untyped
	// prepared value used as a jsonb_build_object argument (SQLSTATE 42P18),
	// so writeAudit must send its provider parameter with an explicit type.
	err := writeAudit(
		context.Background(),
		typedProviderAuditTx{},
		"477004db-72a6-4741-aa3f-bcfaedb50c9e",
		"8204791d-3745-43fc-99c4-570c68c8fda3",
		"INTEGRATION_CONFIGURED",
		"65017477-c0c2-4b42-9fe7-65f322c1c357",
		ProviderResend,
	)
	if err != nil {
		t.Fatalf("writeAudit error = %v, want typed provider parameter", err)
	}
}

// typedProviderAuditTx models the type requirement PostgreSQL enforces at the
// SQL boundary. Embedding pgx.Tx supplies unused pgx methods while Exec
// examines the statement writeAudit sends to PostgreSQL.
type typedProviderAuditTx struct{ pgx.Tx }

func (typedProviderAuditTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if !strings.Contains(sql, "$5::text") {
		return pgconn.CommandTag{}, &pgconn.PgError{Code: "42P18"}
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
