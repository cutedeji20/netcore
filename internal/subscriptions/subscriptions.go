// Package subscriptions owns the subscription lifecycle and its staff-facing
// read model. It keeps access lifecycle rules separate from customer data.
package subscriptions

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidPage = errors.New("subscriptions: invalid page request")
	ErrUnavailable = errors.New("subscriptions: subscription data unavailable")
)

// Subscription is the explicit staff-safe representation used by the list
// endpoint. Tenant, billing-provider, and internal enforcement fields stay
// inside the persistence boundary.
type Subscription struct {
	ID                string
	CustomerID        string
	CustomerNumber    string
	CustomerFirstName string
	CustomerLastName  string
	PlanID            string
	PlanName          string
	Status            Status
	StartsAt          *time.Time
	ExpiresAt         *time.Time
	AutoRenew         bool
	PaymentStatus     string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ListOptions is a bounded, keyset-paginated subscription query. Status is
// optional; an empty value returns every lifecycle state.
type ListOptions struct {
	Limit  int
	Cursor Cursor
	Search string
	Status Status
}

// Cursor selects rows after the final row from the preceding page, ordered by
// created_at descending and then ID descending.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

func (c Cursor) IsZero() bool { return c.CreatedAt.IsZero() || c.ID == "" }

// Page holds a single look-ahead cursor instead of a total count, avoiding
// large-table count queries on every operations screen refresh.
type Page struct {
	Subscriptions []Subscription
	Next          Cursor
	HasMore       bool
}

// Store is the subscription persistence boundary.
type Store interface {
	List(ctx context.Context, tenantID string, options ListOptions) (Page, error)
}
