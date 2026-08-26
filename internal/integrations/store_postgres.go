package integrations

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore persists encrypted provider envelopes through the established
// tenant transaction boundary. Every query keeps an explicit tenant predicate
// in addition to forced RLS.
type PostgresStore struct{ db *database.Pool }

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("integrations: database pool is required")
	}
	return &PostgresStore{db: db}, nil
}

// integrationSaveArgs keeps the positional query arguments aligned with the
// integration_providers insert columns in Save.
func integrationSaveArgs(record Record) []any {
	return []any{
		record.TenantID,
		string(record.Provider),
		record.Envelope.Ciphertext,
		record.Envelope.Nonce,
		record.Envelope.WrappedDEK,
		record.Envelope.KEKKeyID,
		record.SenderEmail,
		record.PaystackMode,
		record.ActivatedAt,
		record.LastTestedAt,
		record.LastTestSucceeded,
		record.UpdatedAt,
		record.UpdatedBy,
	}
}

func (s *PostgresStore) Save(ctx context.Context, record Record) error {
	if s == nil || s.db == nil {
		return ErrStoreUnavailable
	}
	if !validTenantID(record.TenantID) || !record.Provider.Valid() || record.Status != StatusActive || record.UpdatedBy == "" || len(record.Envelope.Ciphertext) == 0 || len(record.Envelope.Nonce) != 12 || len(record.Envelope.WrappedDEK) == 0 || record.Envelope.KEKKeyID == "" {
		return ErrStorePrecondition
	}
	phase := "setup"
	err := s.db.InTenantTx(ctx, record.TenantID, func(tx pgx.Tx) error {
		phase = "upsert"
		var integrationID string
		args := integrationSaveArgs(record)
		err := tx.QueryRow(ctx, `
INSERT INTO integration_providers (
    tenant_id, provider, status, credential_ciphertext, credential_nonce,
    wrapped_dek, kek_key_id, sender_email, paystack_mode, activated_at,
    last_tested_at, last_test_succeeded, updated_at, updated_by, disabled_at
)
VALUES ($1, $2, 'ACTIVE', $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9, $10, $11, $12, $13::uuid, NULL)
ON CONFLICT (tenant_id, provider) DO UPDATE
   SET status = 'ACTIVE',
       credential_ciphertext = EXCLUDED.credential_ciphertext,
       credential_nonce = EXCLUDED.credential_nonce,
       wrapped_dek = EXCLUDED.wrapped_dek,
       kek_key_id = EXCLUDED.kek_key_id,
       sender_email = EXCLUDED.sender_email,
       paystack_mode = EXCLUDED.paystack_mode,
       activated_at = EXCLUDED.activated_at,
       last_tested_at = EXCLUDED.last_tested_at,
       last_test_succeeded = EXCLUDED.last_test_succeeded,
       disabled_at = NULL,
       updated_at = EXCLUDED.updated_at,
       updated_by = EXCLUDED.updated_by
 WHERE integration_providers.tenant_id = $1
   AND integration_providers.provider = $2
RETURNING id::text`,
			args...,
		).Scan(&integrationID)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrStoreUpsert, err)
		}
		phase = "audit"
		if err := writeAudit(ctx, tx, record.TenantID, record.UpdatedBy, "INTEGRATION_CONFIGURED", integrationID, record.Provider); err != nil {
			return fmt.Errorf("%w: %w", ErrStoreAudit, err)
		}
		phase = "commit"
		return nil
	})
	if err != nil {
		switch phase {
		case "setup":
			return fmt.Errorf("%w: %w", ErrStoreTxSetup, err)
		case "commit":
			return fmt.Errorf("%w: %w", ErrStoreTxCommit, err)
		}
		return fmt.Errorf("integrations: save record: %w", err)
	}
	return nil
}

func (s *PostgresStore) List(ctx context.Context, tenantID string) (records []Record, err error) {
	if s == nil || s.db == nil || !validTenantID(tenantID) {
		return nil, ErrStoreUnavailable
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT tenant_id::text, provider, status, COALESCE(sender_email::text, ''),
       COALESCE(paystack_mode, ''), last_tested_at, last_test_succeeded,
       activated_at, updated_at
  FROM integration_providers
 WHERE tenant_id = $1
 ORDER BY provider`, tenantID)
		if err != nil {
			return fmt.Errorf("list integration settings: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var record Record
			var testedAt, activatedAt pgtype.Timestamptz
			var tested pgtype.Bool
			if err := rows.Scan(&record.TenantID, &record.Provider, &record.Status, &record.SenderEmail, &record.PaystackMode, &testedAt, &tested, &activatedAt, &record.UpdatedAt); err != nil {
				return fmt.Errorf("scan integration settings: %w", err)
			}
			if testedAt.Valid {
				record.LastTestedAt = testedAt.Time
				record.LastTestSucceeded = tested.Bool
			}
			if activatedAt.Valid {
				record.ActivatedAt = activatedAt.Time
			}
			records = append(records, record)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("integrations: list records: %w", err)
	}
	return records, nil
}

// LoadActive returns a complete envelope only inside the requested tenant's
// RLS transaction. It is intentionally not used by the dashboard list path.
func (s *PostgresStore) LoadActive(ctx context.Context, tenantID string, provider Provider) (record Record, found bool, err error) {
	if s == nil || s.db == nil || !validTenantID(tenantID) || !provider.Valid() {
		return Record{}, false, ErrStoreUnavailable
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
SELECT tenant_id::text, provider, status, credential_ciphertext, credential_nonce,
       wrapped_dek, kek_key_id, COALESCE(sender_email::text, ''),
       COALESCE(paystack_mode, '')
  FROM integration_providers
 WHERE tenant_id = $1 AND provider = $2 AND status = 'ACTIVE'`, tenantID, string(provider)).Scan(
			&record.TenantID, &record.Provider, &record.Status, &record.Envelope.Ciphertext,
			&record.Envelope.Nonce, &record.Envelope.WrappedDEK, &record.Envelope.KEKKeyID,
			&record.SenderEmail, &record.PaystackMode,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			found = false
			return nil
		}
		if err != nil {
			return fmt.Errorf("load active integration: %w", err)
		}
		found = true
		return nil
	})
	if err != nil {
		return Record{}, false, fmt.Errorf("integrations: load active record: %w", err)
	}
	return record, found, nil
}

func (s *PostgresStore) Disable(ctx context.Context, tenantID string, provider Provider, actorID string, at time.Time) error {
	return s.changeStatus(ctx, tenantID, provider, actorID, at, false)
}

func (s *PostgresStore) Disconnect(ctx context.Context, tenantID string, provider Provider, actorID string, at time.Time) error {
	return s.changeStatus(ctx, tenantID, provider, actorID, at, true)
}

func (s *PostgresStore) changeStatus(ctx context.Context, tenantID string, provider Provider, actorID string, at time.Time, disconnect bool) error {
	if s == nil || s.db == nil || !validTenantID(tenantID) || !provider.Valid() || actorID == "" {
		return ErrStoreUnavailable
	}
	err := s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var query string
		if disconnect {
			query = `UPDATE integration_providers
  SET status = 'DISCONNECTED', credential_ciphertext = NULL, credential_nonce = NULL,
      wrapped_dek = NULL, kek_key_id = NULL, activated_at = NULL, disabled_at = NULL,
      updated_at = $4, updated_by = $3::uuid
 WHERE tenant_id = $1 AND provider = $2`
		} else {
			query = `UPDATE integration_providers
  SET status = 'DISABLED', disabled_at = $4, updated_at = $4, updated_by = $3::uuid
 WHERE tenant_id = $1 AND provider = $2 AND status = 'ACTIVE'`
		}
		var integrationID string
		if err := tx.QueryRow(ctx, query+"\nRETURNING id::text", tenantID, string(provider), actorID, at).Scan(&integrationID); err != nil {
			return ErrStoreUnavailable
		}
		action := "INTEGRATION_DISABLED"
		if disconnect {
			action = "INTEGRATION_DISCONNECTED"
		}
		return writeAudit(ctx, tx, tenantID, actorID, action, integrationID, provider)
	})
	if err != nil {
		return ErrStoreUnavailable
	}
	return nil
}

func writeAudit(ctx context.Context, tx pgx.Tx, tenantID, actorID, action, integrationID string, provider Provider) error {
	if _, err := tx.Exec(ctx, `
INSERT INTO audit_logs (tenant_id, actor_type, actor_id, action, resource_type, resource_id, metadata)
VALUES ($1, 'USER', $2::uuid, $3, 'integration_provider', $4::uuid,
        jsonb_build_object('provider', $5::text))`, tenantID, actorID, action, integrationID, string(provider)); err != nil {
		return fmt.Errorf("write integration audit record: %w", err)
	}
	return nil
}
