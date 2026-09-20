package network

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type tetheringPolicyTx struct {
	pgx.Tx
	calls []networkSQLCall
}
type networkSQLCall struct {
	sql  string
	args []any
}
type policyRow struct {
	enabled bool
	ttl     int
}

func (r policyRow) Scan(dest ...any) error {
	*(dest[0].(*bool)) = r.enabled
	*(dest[1].(*int)) = r.ttl
	return nil
}
func (tx *tetheringPolicyTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	tx.calls = append(tx.calls, networkSQLCall{sql, args})
	return policyRow{false, 64}
}
func (tx *tetheringPolicyTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx.calls = append(tx.calls, networkSQLCall{sql, args})
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func TestSaveTetheringPolicyTxAtomicallyPersistsPolicyAndNamedAudit(t *testing.T) {
	tx := &tetheringPolicyTx{}
	actor := MutationActor{UserID: "44444444-4444-4444-8444-444444444444"}
	policy := TetheringPolicy{Enabled: true, ExpectedClientTTL: 64}
	if err := saveTetheringPolicyTx(context.Background(), tx, networkTestTenantID, actor, policy); err != nil {
		t.Fatal(err)
	}
	if len(tx.calls) != 3 {
		t.Fatalf("calls=%d want select, update, audit", len(tx.calls))
	}
	if !strings.Contains(tx.calls[1].sql, "UPDATE tenant_hotspot_tethering_policies") || !reflect.DeepEqual(tx.calls[1].args, []any{networkTestTenantID, true, 64, actor.UserID}) {
		t.Fatalf("update=%+v", tx.calls[1])
	}
	audit := tx.calls[2]
	for _, fragment := range []string{"HOTSPOT_TETHERING_POLICY_UPDATED", "'old_enabled',$3::boolean", "'old_expected_client_ttl',$4::integer", "'new_enabled',$5::boolean", "'new_expected_client_ttl',$6::integer"} {
		if !strings.Contains(audit.sql, fragment) {
			t.Fatalf("audit query missing %q: %s", fragment, audit.sql)
		}
	}
	if !reflect.DeepEqual(audit.args, []any{networkTestTenantID, actor.UserID, false, 64, true, 64}) {
		t.Fatalf("audit args=%#v", audit.args)
	}
}

func TestNetworkAuditQueryTypesVersionAsBigInt(t *testing.T) {
	path := filepath.Join("store_postgres.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(source), "'version', $8::bigint") {
		t.Fatal("network audit query must cast its version parameter to bigint")
	}
}
