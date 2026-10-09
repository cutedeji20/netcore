package vouchers

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

func TestRedeemForDevicePostgres(t *testing.T) {
	dsn := os.Getenv("NETCORE_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("set NETCORE_TEST_DB_DSN to run the PostgreSQL redemption test")
	}
	ctx := context.Background()
	seed, err := pgx.Connect(ctx, dsn)
	if err != nil { t.Fatal(err) }
	defer seed.Close(ctx)
	pool, err := database.Open(ctx, config.Database{DSN: dsn, MaxConns: 2, StatementTimeout: 10*time.Second, ConnectTimeout: 5*time.Second})
	if err != nil { t.Fatal(err) }
	defer pool.Close()
	store, err := NewPostgresStore(pool)
	if err != nil { t.Fatal(err) }
	redeemer, err := NewRedeemer(store)
	if err != nil { t.Fatal(err) }
	tenantID, planID := uuid.NewString(), uuid.NewString()
	code, err := NewCode()
	if err != nil { t.Fatal(err) }
	hash := HashCode(code)
	_, err = seed.Exec(ctx, `INSERT INTO tenants(id,name,slug,currency) VALUES($1::uuid,'Voucher test',$1::text,'NGN')`, tenantID)
	if err != nil { t.Fatal(err) }
	_, err = seed.Exec(ctx, `INSERT INTO plans(id,tenant_id,name,price_minor,currency,duration_seconds,download_bps,upload_bps,throttle_download_bps,throttle_upload_bps) VALUES($1,$2,'Voucher plan',1000,'NGN',3600,1000000,1000000,100000,100000)`, planID, tenantID)
	if err != nil { t.Fatal(err) }
	_, err = seed.Exec(ctx, `INSERT INTO nas(tenant_id,nasname,radius_source_ip,hotspot_address,shortname,secret_ref) VALUES($1,'10.251.0.1','10.251.0.1','10.251.0.2','voucher-test','secrets/voucher-test')`, tenantID)
	if err != nil { t.Fatal(err) }
	_, err = seed.Exec(ctx, `INSERT INTO vouchers(tenant_id,code_hash,code_prefix,plan_id) VALUES($1,$2,$3,$4)`, tenantID, hash[:], code[:4], planID)
	if err != nil { t.Fatal(err) }
	first, err := redeemer.RedeemForDevice(ctx, tenantID, code, "aa:bb:cc:dd:ee:ff", "10.251.0.2")
	if err != nil { t.Fatal(err) }
	second, err := redeemer.RedeemForDevice(ctx, tenantID, code, "aa:bb:cc:dd:ee:ff", "10.251.0.2")
	if err != nil || second != first { t.Fatalf("repeat redemption: id=%q, error=%v", second, err) }
	if _, err := redeemer.RedeemForDevice(ctx, tenantID, code, "aa:bb:cc:dd:ee:00", "10.251.0.2"); !errors.Is(err, ErrRedeemDenied) { t.Fatalf("other device: %v", err) }
	var count int
	if err := seed.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE tenant_id=$1`, tenantID).Scan(&count); err != nil || count != 1 { t.Fatalf("subscriptions=%d, error=%v", count, err) }
}
