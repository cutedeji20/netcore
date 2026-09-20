package devices

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/netcore-isp/netcore/internal/database"
)

type PostgresStore struct{ db *database.Pool }

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("devices: database pool is required")
	}
	return &PostgresStore{db: db}, nil
}
func (s *PostgresStore) List(ctx context.Context, tenantID, userID string) (devices []Device, err error) {
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT d.id::text, d.normalized_mac, COALESCE(d.hostname, ''), d.status, d.created_at FROM devices d JOIN customers c ON c.id = d.customer_id AND c.tenant_id = d.tenant_id WHERE d.tenant_id = $1 AND c.user_id = $2 AND c.status = 'ACTIVE' ORDER BY d.created_at DESC, d.id DESC`, tenantID, userID)
		if err != nil {
			return fmt.Errorf("devices: list: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var device Device
			if err := rows.Scan(&device.ID, &device.NormalizedMAC, &device.Label, &device.Status, &device.CreatedAt); err != nil {
				return fmt.Errorf("devices: scan: %w", err)
			}
			devices = append(devices, device)
		}
		return rows.Err()
	})
	return devices, err
}
func (s *PostgresStore) Register(ctx context.Context, tenantID, userID string, input Registration) (device Device, err error) {
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var customerID string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM customers WHERE tenant_id = $1 AND user_id = $2 AND status = 'ACTIVE' FOR UPDATE`, tenantID, userID).Scan(&customerID); errors.Is(err, pgx.ErrNoRows) {
			return ErrUnavailable
		} else if err != nil {
			return fmt.Errorf("devices: customer: %w", err)
		}
		err := tx.QueryRow(ctx, `INSERT INTO devices (tenant_id, customer_id, mac_address, normalized_mac, hostname) VALUES ($1, $2, $3, $3, NULLIF($4, '')) RETURNING id::text, normalized_mac, COALESCE(hostname, ''), status, created_at`, tenantID, customerID, input.NormalizedMAC, input.Label).Scan(&device.ID, &device.NormalizedMAC, &device.Label, &device.Status, &device.CreatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrDuplicateMAC
			}
			return fmt.Errorf("devices: register: %w", err)
		}
		return nil
	})
	return device, err
}
