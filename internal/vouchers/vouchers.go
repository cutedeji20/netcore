// Package vouchers owns staff-facing prepaid-voucher inventory read models.
// Voucher code material is deliberately excluded: codes and hashes must never
// cross an ordinary browser API boundary.
package vouchers

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidPage = errors.New("vouchers: invalid page request")
	ErrUnavailable = errors.New("vouchers: voucher data unavailable")
)

// Batch is an aggregated, staff-safe voucher inventory row. BatchID is an
// opaque UUID; code_hash and code_prefix are intentionally absent.
type Batch struct {
	ID        string
	PlanName  string
	Issued    int64
	Redeemed  int64
	Available int64
	Status    string
	ExpiresAt *time.Time
	CreatedAt time.Time
}

// ListOptions is a bounded keyset-paginated batch inventory query.
type ListOptions struct {
	Limit  int
	Cursor Cursor
	Search string
}

type Cursor struct {
	CreatedAt time.Time
	ID        string
}

func (c Cursor) IsZero() bool { return c.CreatedAt.IsZero() || c.ID == "" }

type Page struct {
	Batches []Batch
	Next    Cursor
	HasMore bool
}

// Store is the voucher persistence boundary.
type Store interface {
	List(ctx context.Context, tenantID string, options ListOptions) (Page, error)
}
