package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *PostgresStore) PreviewTestDataReset(ctx context.Context, tenantID string) (preview TestDataResetPreview, err error) {
	if s == nil || s.db == nil || !validUUID(tenantID) {
		return preview, ErrUnavailable
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		payload, err := queryResetJSON(ctx, tx, `SELECT tenant_test_data_reset_preview($1::uuid)`, tenantID)
		if err != nil {
			return fmt.Errorf("preview test-data reset: %w", err)
		}
		if err := json.Unmarshal(payload, &preview); err != nil {
			return fmt.Errorf("decode test-data reset preview: %w", err)
		}
		return nil
	})
	return preview, err
}

func (s *PostgresStore) ExecuteTestDataReset(ctx context.Context, tenantID, actorID, backupReference, reason string) (result TestDataResetResult, err error) {
	if s == nil || s.db == nil || !validUUID(tenantID) || !validUUID(actorID) {
		return result, ErrUnavailable
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		payload, err := queryResetJSON(ctx, tx, `SELECT tenant_test_data_reset_execute($1::uuid, $2::uuid, $3, $4)`, tenantID, actorID, backupReference, reason)
		if err != nil {
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) && databaseError.Code == "P0001" {
				return ErrResetBlocked
			}
			return fmt.Errorf("execute test-data reset: %w", err)
		}
		if err := json.Unmarshal(payload, &result); err != nil {
			return fmt.Errorf("decode test-data reset result: %w", err)
		}
		return nil
	})
	return result, err
}

func queryResetJSON(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]byte, error) {
	var payload []byte
	if err := tx.QueryRow(ctx, query, args...).Scan(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

var _ TestDataResetStore = (*PostgresStore)(nil)
