package security

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/netcore-isp/netcore/internal/database"
)

type Overview struct {
	ActiveCustomers, OnlineSessions, Attention int64
	CollectedTodayMinor                        int64
	Recent                                     []ActivityEvent
}
type OverviewStore interface {
	Overview(context.Context, string) (Overview, error)
}
type OverviewPostgresStore struct{ db *database.Pool }

func NewOverviewPostgresStore(db *database.Pool) (*OverviewPostgresStore, error) {
	if db == nil {
		return nil, errors.New("security: database pool is required")
	}
	return &OverviewPostgresStore{db: db}, nil
}
func (s *OverviewPostgresStore) Overview(ctx context.Context, tenantID string) (out Overview, err error) {
	if tenantID == "" {
		return out, ErrActivityUnavailable
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM customers WHERE tenant_id=$1 AND status='ACTIVE'),
 (SELECT count(*) FROM sessions WHERE tenant_id=$1 AND status <> 'CLOSED'),
 (SELECT COALESCE(sum(amount_minor),0) FROM payments WHERE tenant_id=$1 AND status='SUCCESS' AND created_at >= date_trunc('day', now())),
 (SELECT count(*) FROM subscriptions WHERE tenant_id=$1 AND status='ACTIVE' AND expires_at <= now() + interval '24 hours')`, tenantID).Scan(&out.ActiveCustomers, &out.OnlineSessions, &out.CollectedTodayMinor, &out.Attention)
	})
	if err != nil {
		return Overview{}, fmt.Errorf("security: overview: %w", err)
	}
	return out, nil
}
