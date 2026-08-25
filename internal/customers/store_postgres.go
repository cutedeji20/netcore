package customers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/netcore-isp/netcore/internal/database"
)

// PostgresStore reads customer rows through the transaction-scoped tenant RLS
// boundary. The explicit tenant predicate remains present in every query as a
// second guard against accidental cross-tenant access.
type PostgresStore struct{ db *database.Pool }

const customerCreateSQL = `
INSERT INTO customers (tenant_id, customer_number, status, first_name, last_name, email, phone)
VALUES ($1, 'CUS-' || upper(replace(gen_random_uuid()::text, '-', '')), 'ACTIVE', $2, $3, $4, NULLIF($5, ''))
RETURNING id::text, customer_number, status, COALESCE(first_name, ''),
          COALESCE(last_name, ''), COALESCE(phone, ''), COALESCE(email::text, ''), created_at, updated_at`

const customerUpdateSQL = `
UPDATE customers
   SET first_name = $3, last_name = $4, email = $5, phone = NULLIF($6, ''), updated_at = now()
 WHERE tenant_id = $1 AND id = $2::uuid
RETURNING id::text, customer_number, status, COALESCE(first_name, ''),
          COALESCE(last_name, ''), COALESCE(phone, ''), COALESCE(email::text, ''), created_at, updated_at`

const customerDeactivateSQL = `
UPDATE customers
   SET status = 'SUSPENDED', updated_at = now()
 WHERE tenant_id = $1 AND id = $2::uuid
RETURNING id::text, customer_number, status, COALESCE(first_name, ''),
          COALESCE(last_name, ''), COALESCE(phone, ''), COALESCE(email::text, ''), created_at, updated_at`

const customerAuditSQL = `
INSERT INTO audit_logs (tenant_id, actor_type, actor_id, action, resource_type, resource_id, ip_address, user_agent, metadata)
VALUES ($1, 'USER', $2, $3, 'customers', $4, NULLIF($5, '')::inet, NULLIF($6, ''), '{}'::jsonb)`

func NewPostgresStore(db *database.Pool) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("customers: database pool is required")
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) List(ctx context.Context, tenantID string, options ListOptions) (page Page, err error) {
	if tenantID == "" || options.Limit < 1 {
		return Page{}, ErrInvalidPage
	}
	options.Search = strings.TrimSpace(options.Search)

	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
SELECT id::text,
       customer_number,
       status,
       COALESCE(first_name, ''),
       COALESCE(last_name, ''),
       COALESCE(phone, ''),
       COALESCE(email::text, ''),
       created_at,
       updated_at
  FROM customers
 WHERE tenant_id = $1
   AND (
       $2 = ''
       OR customer_number ILIKE '%' || $2 || '%'
       OR COALESCE(first_name, '') ILIKE '%' || $2 || '%'
       OR COALESCE(last_name, '') ILIKE '%' || $2 || '%'
       OR COALESCE(phone, '') ILIKE '%' || $2 || '%'
       OR COALESCE(email::text, '') ILIKE '%' || $2 || '%'
   )
   AND (
       $3::timestamptz IS NULL
       OR (created_at, id) < ($3::timestamptz, $4::uuid)
   )
 ORDER BY created_at DESC, id DESC
 LIMIT $5`,
			tenantID,
			options.Search,
			nullableCursorTime(options.Cursor),
			nullableCursorID(options.Cursor),
			options.Limit+1,
		)
		if err != nil {
			return fmt.Errorf("query customers: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var customer Customer
			if err := rows.Scan(
				&customer.ID,
				&customer.CustomerNumber,
				&customer.Status,
				&customer.FirstName,
				&customer.LastName,
				&customer.Phone,
				&customer.Email,
				&customer.CreatedAt,
				&customer.UpdatedAt,
			); err != nil {
				return fmt.Errorf("scan customer: %w", err)
			}
			page.Customers = append(page.Customers, customer)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate customers: %w", err)
		}
		if len(page.Customers) <= options.Limit {
			return nil
		}

		page.HasMore = true
		page.Customers = page.Customers[:options.Limit]
		last := page.Customers[len(page.Customers)-1]
		page.Next = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
		return nil
	})
	if err != nil {
		return Page{}, err
	}
	return page, nil
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

func (s *PostgresStore) Create(ctx context.Context, tenantID string, actor MutationActor, input WriteInput) (customer Customer, err error) {
	if tenantID == "" || actor.UserID == "" || input.NormalizeAndValidate() != nil {
		return Customer{}, ErrInvalidInput
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, customerCreateSQL,
			tenantID, input.FirstName, input.LastName, input.Email, input.Phone,
		)
		if err := scanCustomer(row, &customer); err != nil {
			return customerWriteError("insert customer", err)
		}
		return writeCustomerAudit(ctx, tx, tenantID, actor, "CUSTOMER_CREATED", customer.ID)
	})
	if err != nil {
		return Customer{}, err
	}
	return customer, nil
}

func (s *PostgresStore) Update(ctx context.Context, tenantID, customerID string, actor MutationActor, input WriteInput) (customer Customer, err error) {
	if tenantID == "" || !validUUID(customerID) || actor.UserID == "" || input.NormalizeAndValidate() != nil {
		return Customer{}, ErrInvalidInput
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, customerUpdateSQL,
			tenantID, customerID, input.FirstName, input.LastName, input.Email, input.Phone,
		)
		if err := scanCustomer(row, &customer); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return customerWriteError("update customer", err)
		}
		return writeCustomerAudit(ctx, tx, tenantID, actor, "CUSTOMER_UPDATED", customer.ID)
	})
	if err != nil {
		return Customer{}, err
	}
	return customer, nil
}

// Deactivate intentionally changes only the customer lifecycle state. It
// neither destroys data nor cascades into subscriptions, router sessions, or
// any portal identity state.
func (s *PostgresStore) Deactivate(ctx context.Context, tenantID, customerID string, actor MutationActor) (customer Customer, err error) {
	if tenantID == "" || !validUUID(customerID) || actor.UserID == "" {
		return Customer{}, ErrInvalidInput
	}
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, customerDeactivateSQL, tenantID, customerID)
		if err := scanCustomer(row, &customer); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("deactivate customer: %w", err)
		}
		return writeCustomerAudit(ctx, tx, tenantID, actor, "CUSTOMER_DEACTIVATED", customer.ID)
	})
	if err != nil {
		return Customer{}, err
	}
	return customer, nil
}

func scanCustomer(row pgx.Row, customer *Customer) error {
	return row.Scan(
		&customer.ID, &customer.CustomerNumber, &customer.Status, &customer.FirstName,
		&customer.LastName, &customer.Phone, &customer.Email, &customer.CreatedAt, &customer.UpdatedAt,
	)
}

func writeCustomerAudit(ctx context.Context, tx pgx.Tx, tenantID string, actor MutationActor, action, customerID string) error {
	_, err := tx.Exec(ctx, customerAuditSQL,
		tenantID, actor.UserID, action, customerID, actor.IP, actor.UserAgent,
	)
	if err != nil {
		return fmt.Errorf("write customer audit record: %w", err)
	}
	return nil
}

func customerWriteError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "customers_tenant_email_key" {
		return ErrDuplicateEmail
	}
	return fmt.Errorf("%s: %w", operation, err)
}
