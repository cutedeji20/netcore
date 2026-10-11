package security

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/netcore-isp/netcore/internal/database"
)

type Overview struct {
	ActiveCustomers, OnlineSessions, Attention int64
	CollectedTodayMinor                        int64
	Recent                                     []ActivityEvent
	CustomerMetrics                            CustomerMetrics
	PlanMetrics                                PlanMetrics
	SubscriptionMetrics                        SubscriptionMetrics
	Traffic                                    TrafficMetrics
}
type TrafficMetrics struct {
	HotspotBytes      int64           `json:"hotspot_bytes"`
	HotspotTodayBytes int64           `json:"hotspot_today_bytes"`
	WANBytes          int64           `json:"wan_bytes"`
	WANTodayBytes     int64           `json:"wan_today_bytes"`
	WANAvailable      bool            `json:"wan_available"`
	ActiveDevices     int64           `json:"active_devices"`
	ObservedHosts     int64           `json:"observed_hosts"`
	Routers           []RouterTraffic `json:"routers"`
}
type RouterTraffic struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	HotspotBytes      int64      `json:"hotspot_bytes"`
	HotspotTodayBytes int64      `json:"hotspot_today_bytes"`
	WANBytes          int64      `json:"wan_bytes"`
	WANTodayBytes     int64      `json:"wan_today_bytes"`
	ActiveDevices     int64      `json:"active_devices"`
	ObservedHosts     *int64     `json:"observed_hosts"`
	WANSampledAt      *time.Time `json:"wan_sampled_at"`
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
		dayStart, dayEnd := lagosDayBounds(time.Now())
		if err := tx.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM customers WHERE tenant_id=$1 AND status='ACTIVE'),
 (SELECT count(*) FROM sessions WHERE tenant_id=$1 AND status='ACTIVE' AND last_interim_at >= now()-interval '10 minutes'),
 (SELECT COALESCE(sum(amount_minor),0) FROM payments WHERE tenant_id=$1 AND status='SUCCESS' AND verified_at >= $2 AND verified_at < $3),
		 (SELECT count(*) FROM subscriptions WHERE tenant_id=$1 AND status='ACTIVE' AND expires_at <= now() + interval '24 hours')`, tenantID, dayStart, dayEnd).Scan(&out.ActiveCustomers, &out.OnlineSessions, &out.CollectedTodayMinor, &out.Attention); err != nil {
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
		if err := tx.QueryRow(ctx, `SELECT
		 (SELECT count(*) FROM subscriptions WHERE tenant_id=$1 AND status='ACTIVE' AND starts_at<=now() AND expires_at>now()),
		 (SELECT count(*) FROM subscriptions WHERE tenant_id=$1 AND status='ACTIVE' AND expires_at>now() AND expires_at<=now()+interval '7 days'),
		 (SELECT count(*) FROM subscriptions WHERE tenant_id=$1 AND status IN ('PENDING','SUSPENDED')),
		 (SELECT COALESCE(avg(extract(epoch FROM (expires_at-starts_at))),0) FROM subscriptions WHERE tenant_id=$1 AND starts_at IS NOT NULL AND expires_at IS NOT NULL)`, tenantID).Scan(&out.SubscriptionMetrics.Active, &out.SubscriptionMetrics.RenewingThisWeek, &out.SubscriptionMetrics.OnHold, &out.SubscriptionMetrics.AverageLifetimeSeconds); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(DISTINCT COALESCE(normalized_mac, device_id::text)) FROM sessions WHERE tenant_id=$1 AND status='ACTIVE' AND last_interim_at>=now()-interval '10 minutes'`, tenantID).Scan(&out.Traffic.ActiveDevices); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT r.id::text, r.name,
 COALESCE((SELECT sum(u.input_bytes+u.output_bytes) FROM router_usage_daily u WHERE u.tenant_id=r.tenant_id AND u.router_id=r.id),0),
 COALESCE((SELECT sum(u.input_bytes+u.output_bytes) FROM router_usage_daily u WHERE u.tenant_id=r.tenant_id AND u.router_id=r.id AND u.usage_day=$2),0),
 COALESCE((SELECT sum(w.rx_bytes+w.tx_bytes) FROM router_wan_daily w WHERE w.tenant_id=r.tenant_id AND w.router_id=r.id),0),
 COALESCE((SELECT sum(w.rx_bytes+w.tx_bytes) FROM router_wan_daily w WHERE w.tenant_id=r.tenant_id AND w.router_id=r.id AND w.usage_day=$2),0),
 (SELECT count(DISTINCT COALESCE(s.normalized_mac,s.device_id::text)) FROM sessions s WHERE s.tenant_id=r.tenant_id AND s.router_id=r.id AND s.status='ACTIVE' AND s.last_interim_at>=now()-interval '10 minutes'),
 (SELECT t.observed_hotspot_hosts FROM router_telemetry_samples t WHERE t.tenant_id=r.tenant_id AND t.router_id=r.id ORDER BY t.sampled_at DESC LIMIT 1),
 (SELECT t.sampled_at FROM router_telemetry_samples t WHERE t.tenant_id=r.tenant_id AND t.router_id=r.id ORDER BY t.sampled_at DESC LIMIT 1)
 FROM routers r WHERE r.tenant_id=$1 AND r.status <> 'RETIRED' ORDER BY r.name`, tenantID, dayStart.In(time.FixedZone("Africa/Lagos", 3600)).Format("2006-01-02"))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var router RouterTraffic
			var hosts *int64
			var sampled pgtype.Timestamptz
			if err := rows.Scan(&router.ID, &router.Name, &router.HotspotBytes, &router.HotspotTodayBytes, &router.WANBytes, &router.WANTodayBytes, &router.ActiveDevices, &hosts, &sampled); err != nil {
				return err
			}
			router.ObservedHosts = hosts
			if sampled.Valid {
				at := sampled.Time.UTC()
				router.WANSampledAt = &at
				out.Traffic.WANAvailable = true
			}
			out.Traffic.HotspotBytes += router.HotspotBytes
			out.Traffic.HotspotTodayBytes += router.HotspotTodayBytes
			out.Traffic.WANBytes += router.WANBytes
			out.Traffic.WANTodayBytes += router.WANTodayBytes
			if hosts != nil {
				out.Traffic.ObservedHosts += *hosts
			}
			out.Traffic.Routers = append(out.Traffic.Routers, router)
		}
		return rows.Err()
	})
	if err != nil {
		return Overview{}, fmt.Errorf("security: overview: %w", err)
	}
	return out, nil
}

func lagosDayBounds(at time.Time) (time.Time, time.Time) {
	location, err := time.LoadLocation("Africa/Lagos")
	if err != nil {
		location = time.FixedZone("WAT", 3600)
	}
	local := at.In(location)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	return start.UTC(), start.AddDate(0, 0, 1).UTC()
}
