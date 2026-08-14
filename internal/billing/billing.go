// Package billing owns staff-facing revenue read models. It deliberately
// separates safe transaction listing from gateway callbacks, reconciliation,
// invoice issuance, and ledger writes.
package billing

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidPage = errors.New("billing: invalid page request")
	ErrUnavailable = errors.New("billing: transaction data unavailable")
)

// Source distinguishes invoice records from payment records in the unified
// billing list.
type Source string

const (
	SourcePayment Source = "PAYMENT"
	SourceInvoice Source = "INVOICE"
)

func IsValidSource(source Source) bool {
	return source == SourcePayment || source == SourceInvoice
}

// Transaction is a staff-safe entry for the revenue operations table. Amounts
// are stored as integer minor units and converted to strings only at the HTTP
// boundary to preserve exact values in browser clients.
type Transaction struct {
	ID                string
	Source            Source
	Reference         string
	CustomerID        string
	CustomerNumber    string
	CustomerFirstName string
	CustomerLastName  string
	AmountMinor       int64
	Currency          string
	Status            string
	RecordedAt        time.Time
	DueAt             *time.Time
	VerifiedAt        *time.Time
}

// ListOptions is a bounded, keyset-paginated unified billing query. Source is
// optional and lets the UI narrow to payments or invoices without a second API.
type ListOptions struct {
	Limit  int
	Cursor Cursor
	Search string
	Source Source
}

// Cursor selects rows after the preceding page's final row, ordered by
// recorded_at, source, and ID, all descending.
type Cursor struct {
	RecordedAt time.Time
	Source     Source
	ID         string
}

func (c Cursor) IsZero() bool {
	return c.RecordedAt.IsZero() || c.Source == "" || c.ID == ""
}

// Page contains one look-ahead cursor rather than a full-table count.
type Page struct {
	Transactions []Transaction
	Next         Cursor
	HasMore      bool
}

// Store is the billing persistence boundary.
type Store interface {
	List(ctx context.Context, tenantID string, options ListOptions) (Page, error)
}
