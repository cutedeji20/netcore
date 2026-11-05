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
	ID       string
	PlanName string
	// OwnerDisplay identifies the account that purchased a batch, or the staff
	// member who issued a manual batch. It never contains a voucher code.
	OwnerDisplay string
	OwnerType    string
	Issued       int64
	Redeemed     int64
	Available    int64
	Status       string
	ExpiresAt    *time.Time
	CreatedAt    time.Time
}

// ListOptions is a bounded keyset-paginated batch inventory query.
type ListOptions struct {
	Limit  int
	Cursor Cursor
	Search string
	Status string
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

// Summary contains aggregate voucher operations data. It deliberately omits
// voucher codes, hashes, customer identities, and device identifiers.
type Summary struct {
	Period             string    `json:"period"`
	StartsAt           time.Time `json:"starts_at"`
	EndsAt             time.Time `json:"ends_at"`
	Issued             int64     `json:"issued"`
	Redeemed           int64     `json:"redeemed"`
	RedeemedFromIssued int64     `json:"-"`
	ExpiredUnused      int64     `json:"expired_unused"`
	Available          int64     `json:"available"`
	Revoked            int64     `json:"revoked"`
	QueuedRedemptions  int64     `json:"queued_redemptions"`
	CustomerBatches    int64     `json:"customer_batches"`
	StaffBatches       int64     `json:"staff_batches"`
	PaidOrders         int64     `json:"paid_orders"`
	CollectedMinor     int64     `json:"collected_minor"`
	DiscountMinor      int64     `json:"discount_minor"`
	Currency           string    `json:"currency"`
}

func (s Summary) RedemptionRateBasisPoints() int64 {
	if s.Issued <= 0 {
		return 0
	}
	return s.RedeemedFromIssued * 10000 / s.Issued
}

// Store is the voucher persistence boundary.
type Store interface {
	List(ctx context.Context, tenantID string, options ListOptions) (Page, error)
	Summary(ctx context.Context, tenantID string, startsAt, endsAt time.Time, period string) (Summary, error)
}
