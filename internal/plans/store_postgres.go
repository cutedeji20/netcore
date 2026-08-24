package plans

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore reads plans in a transaction carrying the tenant RLS setting.
// The active-subscriber aggregate retains its own tenant predicate so the
// result cannot cross a tenant boundary even if historical keys are malformed.
type PostgresStore struct{ db *database.Pool }

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("plans: database pool is required")
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) List(ctx context.Context, tenantID string, options ListOptions) (page Page, err error) {
	if tenantID == "" || options.Limit < 1 || (options.Status != "" && !IsValidStatus(options.Status)) {
		return Page{}, ErrInvalidPage
	}
	options.Search = strings.TrimSpace(options.Search)

	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT p.id::text,
       p.name,
       COALESCE(p.description, ''),
       p.price_minor,
       p.currency,
       p.duration_seconds,
       p.download_bps,
       p.upload_bps,
       p.max_devices,
       p.max_concurrent_sessions,
       p.quota_bytes,
       p.quota_reset_policy,
       p.status,
       COALESCE(counts.active_subscriptions, 0),
       p.created_at,
       p.updated_at
  FROM plans AS p
  LEFT JOIN (
      SELECT plan_id, COUNT(*)::bigint AS active_subscriptions
        FROM subscriptions
       WHERE tenant_id = $1
         AND status = 'ACTIVE'
       GROUP BY plan_id
  ) AS counts
    ON counts.plan_id = p.id
 WHERE p.tenant_id = $1
   AND (
       $2 = ''
       OR p.name ILIKE '%' || $2 || '%'
       OR COALESCE(p.description, '') ILIKE '%' || $2 || '%'
   )
   AND ($3 = '' OR p.status = $3)
   AND (
       $4::timestamptz IS NULL
       OR (p.created_at, p.id) < ($4::timestamptz, $5::uuid)
   )
 ORDER BY p.created_at DESC, p.id DESC
 LIMIT $6`,
			tenantID,
			options.Search,
			string(options.Status),
			nullableCursorTime(options.Cursor),
			nullableCursorID(options.Cursor),
			options.Limit+1,
		)
		if err != nil {
			return fmt.Errorf("query plans: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var plan Plan
			var quotaBytes pgtype.Int8
			if err := rows.Scan(
				&plan.ID,
				&plan.Name,
				&plan.Description,
				&plan.PriceMinor,
				&plan.Currency,
				&plan.DurationSeconds,
				&plan.DownloadBPS,
				&plan.UploadBPS,
				&plan.MaxDevices,
				&plan.MaxConcurrentSessions,
				&quotaBytes,
				&plan.QuotaResetPolicy,
				&plan.Status,
				&plan.ActiveSubscriptions,
				&plan.CreatedAt,
				&plan.UpdatedAt,
			); err != nil {
				return fmt.Errorf("scan plan: %w", err)
			}
			if quotaBytes.Valid {
				value := quotaBytes.Int64
				plan.QuotaBytes = &value
			}
			page.Plans = append(page.Plans, plan)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate plans: %w", err)
		}
		if len(page.Plans) <= options.Limit {
			return nil
		}

		page.HasMore = true
		page.Plans = page.Plans[:options.Limit]
		last := page.Plans[len(page.Plans)-1]
		page.Next = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
		return nil
	})
	if err != nil {
		return Page{}, err
	}
	return page, nil
}

func (s *PostgresStore) Create(ctx context.Context, tenantID string, actor MutationActor, input WriteInput) (plan Plan, err error) {
	if tenantID == "" || actor.UserID == "" || input.NormalizeAndValidate() != nil {
		return Plan{}, ErrInvalidInput
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var quotaBytes pgtype.Int8
		row := tx.QueryRow(ctx, `
INSERT INTO plans (
    tenant_id, name, description, price_minor, currency, duration_seconds,
    download_bps, upload_bps, max_devices, max_concurrent_sessions,
    quota_bytes, quota_reset_policy, quota_exhausted_action, status
)
VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8, $9, $10, $11, $12, 'DISCONNECT', $13)
RETURNING id::text, name, COALESCE(description, ''), price_minor, currency,
          duration_seconds, download_bps, upload_bps, max_devices,
          max_concurrent_sessions, quota_bytes, quota_reset_policy, status,
          created_at, updated_at`,
			tenantID, input.Name, input.Description, input.PriceMinor, input.Currency,
			input.DurationSeconds, input.DownloadBPS, input.UploadBPS, input.MaxDevices,
			input.MaxConcurrentSessions, input.QuotaBytes, input.QuotaResetPolicy, string(input.Status),
		)
		if err := row.Scan(
			&plan.ID, &plan.Name, &plan.Description, &plan.PriceMinor, &plan.Currency,
			&plan.DurationSeconds, &plan.DownloadBPS, &plan.UploadBPS, &plan.MaxDevices,
			&plan.MaxConcurrentSessions, &quotaBytes, &plan.QuotaResetPolicy, &plan.Status,
			&plan.CreatedAt, &plan.UpdatedAt,
		); err != nil {
			return fmt.Errorf("insert plan: %w", err)
		}
		if quotaBytes.Valid {
			value := quotaBytes.Int64
			plan.QuotaBytes = &value
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO audit_logs (tenant_id, actor_type, actor_id, action, resource_type, resource_id, ip_address, user_agent)
VALUES ($1, 'USER', $2, 'PLAN_CREATED', 'plans', $3, NULLIF($4, '')::inet, NULLIF($5, ''))`,
			tenantID, actor.UserID, plan.ID, actor.IP, actor.UserAgent,
		); err != nil {
			return fmt.Errorf("write plan audit record: %w", err)
		}
		return nil
	})
	if err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func (s *PostgresStore) Update(ctx context.Context, tenantID, planID string, actor MutationActor, input WriteInput) (plan Plan, err error) {
	if tenantID == "" || !validUUID(planID) || actor.UserID == "" || input.NormalizeAndValidate() != nil {
		return Plan{}, ErrInvalidInput
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		previous, err := loadPlanForUpdate(ctx, tx, tenantID, planID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load plan for update: %w", err)
		}
		var used bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM subscriptions WHERE tenant_id = $1 AND plan_id = $2::uuid
)`, tenantID, planID).Scan(&used); err != nil {
			return fmt.Errorf("check plan subscriptions: %w", err)
		}
		if used && !sameTerms(previous, input) {
			return ErrTermsLocked
		}

		var quotaBytes pgtype.Int8
		row := tx.QueryRow(ctx, `
UPDATE plans
   SET name = $3,
       description = NULLIF($4, ''),
       price_minor = $5,
       currency = $6,
       duration_seconds = $7,
       download_bps = $8,
       upload_bps = $9,
       max_devices = $10,
       max_concurrent_sessions = $11,
       quota_bytes = $12,
       quota_reset_policy = $13,
       quota_exhausted_action = 'DISCONNECT',
       status = $14,
       updated_at = now()
 WHERE tenant_id = $1 AND id = $2::uuid
RETURNING id::text, name, COALESCE(description, ''), price_minor, currency,
          duration_seconds, download_bps, upload_bps, max_devices,
          max_concurrent_sessions, quota_bytes, quota_reset_policy, status,
          created_at, updated_at`,
			tenantID, planID, input.Name, input.Description, input.PriceMinor, input.Currency,
			input.DurationSeconds, input.DownloadBPS, input.UploadBPS, input.MaxDevices,
			input.MaxConcurrentSessions, input.QuotaBytes, input.QuotaResetPolicy, string(input.Status),
		)
		if err := row.Scan(
			&plan.ID, &plan.Name, &plan.Description, &plan.PriceMinor, &plan.Currency,
			&plan.DurationSeconds, &plan.DownloadBPS, &plan.UploadBPS, &plan.MaxDevices,
			&plan.MaxConcurrentSessions, &quotaBytes, &plan.QuotaResetPolicy, &plan.Status,
			&plan.CreatedAt, &plan.UpdatedAt,
		); err != nil {
			return fmt.Errorf("update plan: %w", err)
		}
		if quotaBytes.Valid {
			value := quotaBytes.Int64
			plan.QuotaBytes = &value
		}
		action := "PLAN_UPDATED"
		if previous.Status != plan.Status {
			if plan.Status == StatusRetired {
				action = "PLAN_RETIRED"
			} else {
				action = "PLAN_PUBLISHED"
			}
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO audit_logs (tenant_id, actor_type, actor_id, action, resource_type, resource_id, ip_address, user_agent)
VALUES ($1, 'USER', $2, $3, 'plans', $4, NULLIF($5, '')::inet, NULLIF($6, ''))`,
			tenantID, actor.UserID, action, plan.ID, actor.IP, actor.UserAgent,
		); err != nil {
			return fmt.Errorf("write plan audit record: %w", err)
		}
		return nil
	})
	if err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func loadPlanForUpdate(ctx context.Context, tx pgx.Tx, tenantID, planID string) (plan Plan, err error) {
	var quotaBytes pgtype.Int8
	err = tx.QueryRow(ctx, `
SELECT id::text, name, COALESCE(description, ''), price_minor, currency,
       duration_seconds, download_bps, upload_bps, max_devices,
       max_concurrent_sessions, quota_bytes, quota_reset_policy, status,
       created_at, updated_at
  FROM plans
 WHERE tenant_id = $1 AND id = $2::uuid
 FOR UPDATE`, tenantID, planID).Scan(
		&plan.ID, &plan.Name, &plan.Description, &plan.PriceMinor, &plan.Currency,
		&plan.DurationSeconds, &plan.DownloadBPS, &plan.UploadBPS, &plan.MaxDevices,
		&plan.MaxConcurrentSessions, &quotaBytes, &plan.QuotaResetPolicy, &plan.Status,
		&plan.CreatedAt, &plan.UpdatedAt,
	)
	if err != nil {
		return Plan{}, err
	}
	if quotaBytes.Valid {
		value := quotaBytes.Int64
		plan.QuotaBytes = &value
	}
	return plan, nil
}

func sameTerms(plan Plan, input WriteInput) bool {
	if plan.Name != input.Name || plan.Description != input.Description || plan.PriceMinor != input.PriceMinor ||
		plan.Currency != input.Currency || plan.DurationSeconds != input.DurationSeconds ||
		plan.DownloadBPS != input.DownloadBPS || plan.UploadBPS != input.UploadBPS ||
		plan.MaxDevices != input.MaxDevices || plan.MaxConcurrentSessions != input.MaxConcurrentSessions ||
		plan.QuotaResetPolicy != input.QuotaResetPolicy {
		return false
	}
	if plan.QuotaBytes == nil || input.QuotaBytes == nil {
		return plan.QuotaBytes == nil && input.QuotaBytes == nil
	}
	return *plan.QuotaBytes == *input.QuotaBytes
}

func nullableCursorTime(cursor Cursor) any {
	if cursor.IsZero() {
		return nil
	}
	return cursor.CreatedAt
}

func nullableCursorID(cursor Cursor) any {
	if cursor.IsZero() {
		return nil
	}
	return cursor.ID
}
