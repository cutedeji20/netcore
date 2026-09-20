package billing

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type billingSettingsTx struct {
	pgx.Tx
	calls []sqlCall
}

type sqlCall struct {
	sql  string
	args []any
}
type scalarRow struct{ value int64 }

func (r scalarRow) Scan(dest ...any) error { *(dest[0].(*int64)) = r.value; return nil }

func (tx *billingSettingsTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	tx.calls = append(tx.calls, sqlCall{sql, args})
	return scalarRow{value: 1200}
}
func (tx *billingSettingsTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx.calls = append(tx.calls, sqlCall{sql, args})
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func TestSaveSettingsTxAtomicallyPersistsChargeAndNamedAudit(t *testing.T) {
	tx := &billingSettingsTx{}
	actor := MutationActor{UserID: "44444444-4444-4444-8444-444444444444"}
	settings := Settings{FixedBankChargeMinor: 1500, Currency: "NGN"}
	if err := saveSettingsTx(context.Background(), tx, billingTestTenantID, actor, settings); err != nil {
		t.Fatal(err)
	}
	if len(tx.calls) != 3 {
		t.Fatalf("calls=%d want select, update, audit", len(tx.calls))
	}
	if !strings.Contains(tx.calls[1].sql, "UPDATE tenant_billing_settings") || !reflect.DeepEqual(tx.calls[1].args, []any{billingTestTenantID, int64(1500), actor.UserID}) {
		t.Fatalf("update=%+v", tx.calls[1])
	}
	audit := tx.calls[2]
	for _, fragment := range []string{"BILLING_BANK_CHARGE_UPDATED", "'old_fixed_bank_charge_minor',$3::bigint", "'new_fixed_bank_charge_minor',$4::bigint"} {
		if !strings.Contains(audit.sql, fragment) {
			t.Fatalf("audit query missing %q: %s", fragment, audit.sql)
		}
	}
	if !reflect.DeepEqual(audit.args, []any{billingTestTenantID, actor.UserID, int64(1200), int64(1500)}) {
		t.Fatalf("audit args=%#v", audit.args)
	}
}
