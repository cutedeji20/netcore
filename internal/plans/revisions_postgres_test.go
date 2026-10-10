package plans

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/netcore-isp/netcore/internal/config"
	"github.com/netcore-isp/netcore/internal/database"
)

func TestReferencedPlanUpdateCreatesNewRevision(t *testing.T) {
	dsn := os.Getenv("NETCORE_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("set NETCORE_TEST_DB_DSN to run PostgreSQL plan revision test")
	}
	ctx := context.Background()
	seed, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close(ctx)
	pool, err := database.Open(ctx, config.Database{DSN: dsn, MaxConns: 2, StatementTimeout: 10 * time.Second, ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	tenantID, planID := uuid.NewString(), uuid.NewString()
	_, err = seed.Exec(ctx, `INSERT INTO tenants(id,name,slug,currency) VALUES($1::uuid,'Plan revision test',$1::text,'NGN')`, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = seed.Exec(ctx, `INSERT INTO plans(id,tenant_id,name,price_minor,currency,duration_seconds,download_bps,upload_bps,max_devices,max_concurrent_sessions,quota_bytes,quota_reset_policy,quota_exhausted_action) VALUES($1,$2,'Old weekly',50000,'NGN',604800,10000000,5000000,2,1,1000000000,'PER_SUBSCRIPTION','DISCONNECT')`, planID, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	var customerID string
	if err := seed.QueryRow(ctx, `INSERT INTO customers(tenant_id,customer_number) VALUES($1,$2) RETURNING id::text`, tenantID, "REV-"+uuid.NewString()).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	_, err = seed.Exec(ctx, `INSERT INTO subscriptions(tenant_id,customer_id,plan_id,status,payment_status,starts_at,expires_at) VALUES($1,$2,$3,'ACTIVE','PAID',now(),now()+interval '7 days')`, tenantID, customerID, planID)
	if err != nil {
		t.Fatal(err)
	}
	quota := int64(2000000000)
	input := WriteInput{Name: "New weekly", Description: "Updated", PriceMinor: 60000, Currency: "NGN", DurationSeconds: 1209600, DownloadBPS: 20000000, UploadBPS: 10000000, MaxDevices: 3, MaxConcurrentSessions: 2, QuotaBytes: &quota, QuotaResetPolicy: "PER_SUBSCRIPTION", Status: StatusActive}
	actor := MutationActor{UserID: uuid.NewString()}
	updated, err := store.Update(ctx, tenantID, planID, actor, input)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID == planID {
		t.Fatal("referenced plan was changed in place")
	}
	var oldName, oldStatus, oldNext string
	var oldPrice, oldDownload, oldQuota int64
	if err := seed.QueryRow(ctx, `SELECT name,status,superseded_by_plan_id::text,price_minor,download_bps,quota_bytes FROM plans WHERE id=$1`, planID).Scan(&oldName, &oldStatus, &oldNext, &oldPrice, &oldDownload, &oldQuota); err != nil {
		t.Fatal(err)
	}
	if oldName != "Old weekly" || oldStatus != "RETIRED" || oldNext != updated.ID || oldPrice != 50000 || oldDownload != 10000000 || oldQuota != 1000000000 {
		t.Fatalf("old plan changed: %s %s %s %d %d %d", oldName, oldStatus, oldNext, oldPrice, oldDownload, oldQuota)
	}
	if updated.Name != "New weekly" || updated.PriceMinor != 60000 || updated.DownloadBPS != 20000000 || updated.DurationSeconds != 1209600 || updated.MaxDevices != 3 {
		t.Fatalf("new revision: %+v", updated)
	}
	if _, err := store.Restore(ctx, tenantID, planID, actor); !errors.Is(err, ErrPlanSuperseded) {
		t.Fatalf("restoring old revision: %v", err)
	}
}
