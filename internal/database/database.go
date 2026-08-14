// Package database owns the PostgreSQL connection pool used by the business
// application. It is deliberately the only place that imports pgxpool so the
// rest of the application depends on a small, controlled boundary.
package database

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netcore-isp/netcore/internal/config"
)

// Pool is the application's PostgreSQL connection pool.
type Pool struct {
	pool *pgxpool.Pool
}

// Tenant is the minimum tenant record needed before a request can enter a
// tenant-scoped transaction.
type Tenant struct {
	ID   string
	Slug string
}

// Open creates and verifies a PostgreSQL connection pool. pgxpool creates
// pools lazily, so the initial Ping is essential: a process must not announce
// that it started when its only readiness-critical dependency is unreachable.
func Open(ctx context.Context, cfg config.Database) (*Pool, error) {
	if cfg.MaxConns < 1 || cfg.MaxConns > math.MaxInt32 {
		return nil, fmt.Errorf("database: max connections must be between 1 and %d", math.MaxInt32)
	}

	poolConfig, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("database: parse DSN: %w", err)
	}
	poolConfig.MaxConns = int32(cfg.MaxConns)
	if poolConfig.ConnConfig.RuntimeParams == nil {
		poolConfig.ConnConfig.RuntimeParams = make(map[string]string)
	}
	poolConfig.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(cfg.StatementTimeout.Milliseconds(), 10)

	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(connectCtx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("database: create pool: %w", err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: initial ping: %w", err)
	}

	return &Pool{pool: pool}, nil
}

// Ping checks that PostgreSQL can serve queries. It satisfies health.Checker
// without coupling the health package to pgx.
func (p *Pool) Ping(ctx context.Context) error {
	if p == nil || p.pool == nil {
		return errors.New("database: pool is not initialized")
	}
	return p.pool.Ping(ctx)
}

// Close releases the pool's connections. It is safe to call more than once.
func (p *Pool) Close() {
	if p != nil && p.pool != nil {
		p.pool.Close()
	}
}

// ResolveActiveTenant looks up the public tenant identifier used at the login
// boundary. It intentionally returns no customer or credential data. All
// subsequent work must use InTenantTx, which activates forced RLS.
func (p *Pool) ResolveActiveTenant(ctx context.Context, slug string) (Tenant, bool, error) {
	if p == nil || p.pool == nil {
		return Tenant{}, false, errors.New("database: pool is not initialized")
	}
	var tenant Tenant
	err := p.pool.QueryRow(ctx,
		"SELECT id::text, slug FROM tenants WHERE slug = $1 AND status = 'ACTIVE'", slug,
	).Scan(&tenant.ID, &tenant.Slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tenant{}, false, nil
	}
	if err != nil {
		return Tenant{}, false, fmt.Errorf("database: resolve tenant: %w", err)
	}
	return tenant, true, nil
}

// InTenantTx runs fn inside a transaction whose tenant context is set locally.
// PostgreSQL resets this setting when the transaction finishes, preventing a
// pooled connection from leaking one request's tenant into the next one.
//
// Repositories must still include tenant_id predicates in their queries. This
// is defense in depth with the forced RLS policies in migration 0004.
func (p *Pool) InTenantTx(ctx context.Context, tenantID string, fn func(pgx.Tx) error) (err error) {
	if p == nil || p.pool == nil {
		return errors.New("database: pool is not initialized")
	}
	if tenantID == "" {
		return errors.New("database: tenant ID is required")
	}
	if fn == nil {
		return errors.New("database: transaction function is required")
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("database: begin tenant transaction: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) && err == nil {
			err = fmt.Errorf("database: rollback tenant transaction: %w", rollbackErr)
		}
	}()

	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		return fmt.Errorf("database: set tenant context: %w", err)
	}
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("database: commit tenant transaction: %w", err)
	}
	return nil
}

// InSystemTx is for the very small set of globally-addressed operational
// records that have no tenant key at ingress (for example, a signed payment
// webhook event). Callers must resolve a tenant through a narrowly scoped
// SECURITY DEFINER function before reading or changing tenant-owned rows.
// It must never be used as a convenience replacement for InTenantTx.
func (p *Pool) InSystemTx(ctx context.Context, fn func(pgx.Tx) error) (err error) {
	if p == nil || p.pool == nil {
		return errors.New("database: pool is not initialized")
	}
	if fn == nil {
		return errors.New("database: transaction function is required")
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("database: begin system transaction: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) && err == nil {
			err = fmt.Errorf("database: rollback system transaction: %w", rollbackErr)
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("database: commit system transaction: %w", err)
	}
	return nil
}
