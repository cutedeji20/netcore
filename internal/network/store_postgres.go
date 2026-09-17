package network

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore reads routers in a transaction carrying the tenant RLS
// setting. Tenant predicates on sites and NAS rows prevent a historical
// cross-tenant relationship from leaking network inventory.
type PostgresStore struct{ db *database.Pool }

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("network: database pool is required")
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) List(ctx context.Context, tenantID string, options ListOptions) (page Page, err error) {
	if tenantID == "" || options.Limit < 1 || (options.Status != "" && !IsValidRouterStatus(options.Status)) {
		return Page{}, ErrInvalidPage
	}
	options.Search = strings.TrimSpace(options.Search)

	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT r.id::text,
       r.name,
       COALESCE(s.name, 'Unassigned'),
       r.status,
       aaa.status,
       aaa.verified,
       r.last_seen_at
  FROM routers AS r
  LEFT JOIN sites AS s
    ON s.id = r.site_id
   AND s.tenant_id = r.tenant_id
  CROSS JOIN LATERAL (
      SELECT CASE
          WHEN bool_or(n.status = 'ACTIVE') THEN 'ACTIVE'
          WHEN COUNT(*) > 0 THEN 'DISABLED'
          ELSE 'NOT_CONFIGURED'
      END AS status,
      COALESCE(bool_or(c.verified_at IS NOT NULL), false) AS verified
        FROM nas AS n
   LEFT JOIN router_radius_credentials AS c
     ON c.nas_id = n.id
    AND c.tenant_id = n.tenant_id
       WHERE n.tenant_id = r.tenant_id
         AND n.router_id = r.id
  ) AS aaa
 WHERE r.tenant_id = $1
   AND (
       $2 = ''
       OR r.name ILIKE '%' || $2 || '%'
       OR COALESCE(s.name, '') ILIKE '%' || $2 || '%'
   )
   AND ($3 = '' OR r.status = $3)
   AND ($4::text IS NULL OR r.name > $4::text)
 ORDER BY r.name ASC
 LIMIT $5`,
			tenantID,
			options.Search,
			string(options.Status),
			nullableCursorName(options.Cursor),
			options.Limit+1,
		)
		if err != nil {
			return fmt.Errorf("query routers: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var router Router
			var lastSeenAt pgtype.Timestamptz
			if err := rows.Scan(
				&router.ID,
				&router.Name,
				&router.SiteName,
				&router.Status,
				&router.AAAStatus,
				&router.AAAVerified,
				&lastSeenAt,
			); err != nil {
				return fmt.Errorf("scan router: %w", err)
			}
			if lastSeenAt.Valid {
				value := lastSeenAt.Time.UTC()
				router.LastSeenAt = &value
			}
			page.Routers = append(page.Routers, router)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate routers: %w", err)
		}
		if len(page.Routers) <= options.Limit {
			return nil
		}

		page.HasMore = true
		page.Routers = page.Routers[:options.Limit]
		page.Next = Cursor{Name: page.Routers[len(page.Routers)-1].Name}
		return nil
	})
	if err != nil {
		return Page{}, err
	}
	return page, nil
}

func nullableCursorName(cursor Cursor) any {
	if cursor.IsZero() {
		return nil
	}
	return cursor.Name
}

// CreateRouter creates a provisioning router and its disabled NAS together so
// no partially configured device can be accepted by the RADIUS policy.
func (s *PostgresStore) CreateRouter(ctx context.Context, tenantID string, actor MutationActor, input RouterCreateInput) (result AAAConfiguration, err error) {
	if s == nil || s.db == nil || !validNetworkID(tenantID) || !validNetworkID(actor.UserID) || input.NormalizeAndValidate() != nil {
		return AAAConfiguration{}, ErrInvalidRouterInput
	}
	routerID, nasID := uuid.NewString(), uuid.NewString()
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO routers (id, tenant_id, site_id, name, management_ip, status, credential_ref, radius_secret_ref)
VALUES ($1::uuid, $2::uuid, NULLIF($3, '')::uuid, $4, $5::inet, 'PROVISIONING', $6, $7)`,
			routerID, tenantID, input.SiteID, input.Name, input.ManagementIP,
			"network/router/"+routerID+"/management-credential", "network/nas/"+nasID+"/radius-shared-secret"); err != nil {
			return fmt.Errorf("insert router: %w", err)
		}
		if err := tx.QueryRow(ctx, `INSERT INTO nas (id, tenant_id, router_id, nasname, hotspot_address, shortname, secret_ref, radius_source_ip, status)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::inet, $5::inet, $6, $7, $8::inet, 'DISABLED')
RETURNING id::text, hotspot_address::text, radius_source_ip::text, shortname, status`,
			nasID, tenantID, routerID, input.NASIPAddress, input.NASIPAddress, input.Name, "network/nas/"+nasID+"/radius-shared-secret", input.RadiusSourceIP).
			Scan(&result.NASID, &result.NASIPAddress, &result.RadiusSourceIP, &result.ShortName, &result.Status); err != nil {
			return fmt.Errorf("insert NAS: %w", err)
		}
		result.RouterID = routerID
		return writeNetworkAudit(ctx, tx, tenantID, actor, "NETWORK_ROUTER_CREATED", routerID, nasID, input.NASIPAddress, input.RadiusSourceIP, 0)
	})
	if err != nil {
		return AAAConfiguration{}, err
	}
	return result, nil
}

func (s *PostgresStore) LoadAAA(ctx context.Context, tenantID, routerID string) (result AAAConfiguration, err error) {
	if s == nil || s.db == nil || !validNetworkID(tenantID) || !validNetworkID(routerID) {
		return AAAConfiguration{}, ErrNotFound
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT r.id::text, n.id::text, n.hotspot_address::text, n.radius_source_ip::text, n.shortname, n.status, COALESCE(c.version, 0), COALESCE(c.verified_at IS NOT NULL, false)
FROM routers r JOIN nas n ON n.router_id = r.id AND n.tenant_id = r.tenant_id
LEFT JOIN router_radius_credentials c ON c.nas_id = n.id AND c.tenant_id = n.tenant_id
WHERE r.tenant_id = $1::uuid AND r.id = $2::uuid`, tenantID, routerID).
			Scan(&result.RouterID, &result.NASID, &result.NASIPAddress, &result.RadiusSourceIP, &result.ShortName, &result.Status, &result.Version, &result.Verified)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return AAAConfiguration{}, err
	}
	return result, nil
}

func (s *PostgresStore) SaveCredential(ctx context.Context, actor MutationActor, record RadiusCredentialRecord) error {
	if s == nil || s.db == nil || !validNetworkID(actor.UserID) || !record.Valid() {
		return ErrInvalidEnvelope
	}
	return s.db.InTenantTx(ctx, record.TenantID, func(tx pgx.Tx) error {
		var saved int64
		err := tx.QueryRow(ctx, `INSERT INTO router_radius_credentials (nas_id, tenant_id, secret_ciphertext, secret_nonce, wrapped_dek, kek_key_id, version, rotated_at, rotated_by)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, now(), $8::uuid)
ON CONFLICT (nas_id) DO UPDATE SET secret_ciphertext=EXCLUDED.secret_ciphertext, secret_nonce=EXCLUDED.secret_nonce, wrapped_dek=EXCLUDED.wrapped_dek, kek_key_id=EXCLUDED.kek_key_id, version=EXCLUDED.version, updated_at=now(), rotated_at=now(), rotated_by=EXCLUDED.rotated_by, verified_at=NULL, verified_by=NULL
WHERE router_radius_credentials.tenant_id = EXCLUDED.tenant_id AND router_radius_credentials.version = EXCLUDED.version - 1
RETURNING version`, record.NASID, record.TenantID, record.Envelope.Ciphertext, record.Envelope.Nonce, record.Envelope.WrappedDEK, record.Envelope.KEKKeyID, record.Version, actor.UserID).Scan(&saved)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return fmt.Errorf("save NAS credential: %w", err)
		}
		action := "NETWORK_AAA_EXPORTED"
		if saved > 1 {
			action = "NETWORK_AAA_ROTATED"
		}
		return writeNetworkAudit(ctx, tx, record.TenantID, actor, action, "", record.NASID, "", "", saved)
	})
}

func (s *PostgresStore) MarkVerified(ctx context.Context, tenantID, routerID string, actor MutationActor) error {
	if s == nil || s.db == nil || !validNetworkID(tenantID) || !validNetworkID(routerID) || !validNetworkID(actor.UserID) {
		return ErrInvalidRouterInput
	}
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var nasID string
		err := tx.QueryRow(ctx, `UPDATE router_radius_credentials c SET verified_at=now(), verified_by=$3::uuid, updated_at=now()
FROM nas n WHERE c.nas_id=n.id AND c.tenant_id=n.tenant_id AND n.tenant_id=$1::uuid AND n.router_id=$2::uuid
RETURNING n.id::text`, tenantID, routerID, actor.UserID).Scan(&nasID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("mark NAS verification: %w", err)
		}
		return writeNetworkAudit(ctx, tx, tenantID, actor, "NETWORK_AAA_TEST_CONFIRMED", routerID, nasID, "", "", 0)
	})
}

func (s *PostgresStore) SetNASStatus(ctx context.Context, tenantID, routerID string, actor MutationActor, status NASStatus) error {
	if s == nil || s.db == nil || !validNetworkID(tenantID) || !validNetworkID(routerID) || !validNetworkID(actor.UserID) || (status != NASStatusActive && status != NASStatusDisabled) {
		return ErrInvalidRouterInput
	}
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var nasID string
		err := tx.QueryRow(ctx, `UPDATE nas n SET status=$3, updated_at=now() FROM routers r
WHERE n.tenant_id=$1::uuid AND n.router_id=$2::uuid AND r.id=n.router_id AND r.tenant_id=n.tenant_id
AND ($3 <> 'ACTIVE' OR EXISTS (SELECT 1 FROM router_radius_credentials c WHERE c.nas_id=n.id AND c.tenant_id=n.tenant_id AND c.verified_at IS NOT NULL)) RETURNING n.id::text`, tenantID, routerID, string(status)).Scan(&nasID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("set NAS status: %w", err)
		}
		action := "NETWORK_AAA_DISABLED"
		if status == NASStatusActive {
			action = "NETWORK_AAA_ACTIVATED"
		}
		return writeNetworkAudit(ctx, tx, tenantID, actor, action, routerID, nasID, "", "", 0)
	})
}

func writeNetworkAudit(ctx context.Context, tx pgx.Tx, tenantID string, actor MutationActor, action, routerID, nasID, nasIP, sourceIP string, version int64) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs (tenant_id, actor_type, actor_id, action, resource_type, resource_id, metadata, ip_address, user_agent)
VALUES ($1::uuid, 'USER', $2::uuid, $3, 'nas', NULLIF($4, '')::uuid, jsonb_build_object('router_id', NULLIF($5, ''), 'nas_ip_address', NULLIF($6, ''), 'radius_source_ip', NULLIF($7, ''), 'version', $8::bigint), NULLIF($9, '')::inet, NULLIF($10, ''))`, tenantID, actor.UserID, action, nasID, routerID, nasIP, sourceIP, version, actor.IP, actor.UserAgent)
	if err != nil {
		return fmt.Errorf("write network audit record: %w", err)
	}
	return nil
}
