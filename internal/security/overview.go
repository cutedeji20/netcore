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
	CustomerMetrics                            CustomerMetrics
	PlanMetrics                                PlanMetrics
	SubscriptionMetrics                        SubscriptionMetrics
}
type CustomerMetrics struct {
	Active            int64 `json:"active"`
	NewThisMonth      int64 `json:"new_this_month"`
	NeedsReview       int64 `json:"needs_review"`
	WithoutActivePlan int64 `json:"without_active_plan"`
}
type PlanMetrics struct {
	Published     int64  `json:"published"`
	Retired       int64  `json:"retired"`
	MostSelected  string `json:"most_selected"`
	HighestGrowth string `json:"highest_growth"`
}
type SubscriptionMetrics struct {
	Active                 int64   `json:"active"`
	RenewingThisWeek       int64   `json:"renewing_this_week"`
	OnHold                 int64   `json:"on_hold"`
	AverageLifetimeSeconds float64 `json:"average_lifetime_seconds"`
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
		if err := tx.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM customers WHERE tenant_id=$1 AND status='ACTIVE'),
 (SELECT count(*) FROM sessions WHERE tenant_id=$1 AND status <> 'CLOSED'),
 (SELECT COALESCE(sum(amount_minor),0) FROM payments WHERE tenant_id=$1 AND status='SUCCESS' AND created_at >= date_trunc('day', now())),
		 (SELECT count(*) FROM subscriptions WHERE tenant_id=$1 AND status='ACTIVE' AND expires_at <= now() + interval '24 hours')`, tenantID).Scan(&out.ActiveCustomers, &out.OnlineSessions, &out.CollectedTodayMinor, &out.Attention); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT
		 (SELECT count(*) FROM customers WHERE tenant_id=$1 AND status='ACTIVE'),
		 (SELECT count(*) FROM customers WHERE tenant_id=$1 AND created_at >= date_trunc('month', now())),
		 (SELECT count(*) FROM customers WHERE tenant_id=$1 AND status='SUSPENDED'),
		 (SELECT count(*) FROM customers c WHERE c.tenant_id=$1 AND c.status='ACTIVE' AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.tenant_id=c.tenant_id AND s.customer_id=c.id AND s.status='ACTIVE' AND s.starts_at<=now() AND s.expires_at>now()))`, tenantID).Scan(&out.CustomerMetrics.Active, &out.CustomerMetrics.NewThisMonth, &out.CustomerMetrics.NeedsReview, &out.CustomerMetrics.WithoutActivePlan); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT
		 (SELECT count(*) FROM plans WHERE tenant_id=$1 AND status='ACTIVE'),
		 (SELECT count(*) FROM plans WHERE tenant_id=$1 AND status='RETIRED'),
		 COALESCE((SELECT p.name FROM plans p LEFT JOIN subscriptions s ON s.plan_id=p.id AND s.tenant_id=p.tenant_id AND s.status='ACTIVE' AND s.starts_at<=now() AND s.expires_at>now() WHERE p.tenant_id=$1 GROUP BY p.id ORDER BY count(s.id) DESC, p.name LIMIT 1), '—'),
		 COALESCE((SELECT p.name FROM plans p LEFT JOIN subscriptions s ON s.plan_id=p.id AND s.tenant_id=p.tenant_id AND s.created_at>=now()-interval '30 days' WHERE p.tenant_id=$1 GROUP BY p.id ORDER BY count(s.id) DESC, p.name LIMIT 1), '—')`, tenantID).Scan(&out.PlanMetrics.Published, &out.PlanMetrics.Retired, &out.PlanMetrics.MostSelected, &out.PlanMetrics.HighestGrowth); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT
		 (SELECT count(*) FROM subscriptions WHERE tenant_id=$1 AND status='ACTIVE' AND starts_at<=now() AND expires_at>now()),
		 (SELECT count(*) FROM subscriptions WHERE tenant_id=$1 AND status='ACTIVE' AND expires_at>now() AND expires_at<=now()+interval '7 days'),
		 (SELECT count(*) FROM subscriptions WHERE tenant_id=$1 AND status IN ('PENDING','SUSPENDED')),
		 (SELECT COALESCE(avg(extract(epoch FROM (expires_at-starts_at))),0) FROM subscriptions WHERE tenant_id=$1 AND starts_at IS NOT NULL AND expires_at IS NOT NULL)`, tenantID).Scan(&out.SubscriptionMetrics.Active, &out.SubscriptionMetrics.RenewingThisWeek, &out.SubscriptionMetrics.OnHold, &out.SubscriptionMetrics.AverageLifetimeSeconds)
	})
	if err != nil {
		return Overview{}, fmt.Errorf("security: overview: %w", err)
	}
	return out, nil
}
