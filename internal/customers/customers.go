// Package customers owns customer lifecycle and customer-facing account data.
// It deliberately does not depend on subscriptions or payments: a customer
// exists before either one, and cross-module read models belong at the API
// boundary rather than inside this package.
package customers

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidPage = errors.New("customers: invalid page request")
	ErrUnavailable = errors.New("customers: customer data unavailable")
)

// Customer is the explicit, staff-safe representation returned by the first
// read API. It does not expose tenant IDs, user IDs, password metadata or any
// internal security state.
type Customer struct {
	ID             string
	CustomerNumber string
	Status         string
	FirstName      string
	LastName       string
	Phone          string
	Email          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ListOptions is intentionally a small keyset pagination contract. The
// cursor is opaque to clients; limit is bounded by the HTTP boundary.
type ListOptions struct {
	Limit  int
	Cursor Cursor
	Search string
}

// Cursor selects rows after the final row from the preceding page, ordered by
// created_at descending and then ID descending.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

func (c Cursor) IsZero() bool { return c.CreatedAt.IsZero() || c.ID == "" }

// Page contains only one look-ahead token rather than a COUNT(*) result. That
// keeps the UI responsive as the customer table grows.
type Page struct {
	Customers []Customer
	Next      Cursor
	HasMore   bool
}

// Store is the customer persistence boundary.
type Store interface {
	List(ctx context.Context, tenantID string, options ListOptions) (Page, error)
}
