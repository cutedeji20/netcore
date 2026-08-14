// Package plans owns the sellable service catalogue. Subscription lifecycle
// reads plan terms but plan management remains the authoritative boundary for
// prices, speeds, quotas, and access limits.
package plans

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidPage = errors.New("plans: invalid page request")
	ErrUnavailable = errors.New("plans: plan data unavailable")
)

// Status is the publication state of a plan.
type Status string

const (
	StatusActive  Status = "ACTIVE"
	StatusRetired Status = "RETIRED"
)

func IsValidStatus(status Status) bool {
	return status == StatusActive || status == StatusRetired
}

// Plan is the staff-safe plan catalogue read model. Money remains in integer
// minor units all the way to the HTTP response boundary.
type Plan struct {
	ID                    string
	Name                  string
	Description           string
	PriceMinor            int64
	Currency              string
	DurationSeconds       int64
	DownloadBPS           int64
	UploadBPS             int64
	MaxDevices            int
	MaxConcurrentSessions int
	QuotaBytes            *int64
	QuotaResetPolicy      string
	Status                Status
	ActiveSubscriptions   int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// ListOptions is a bounded, keyset-paginated catalogue query. An empty Status
// includes both published and retired plans.
type ListOptions struct {
	Limit  int
	Cursor Cursor
	Search string
	Status Status
}

// Cursor selects rows after the final preceding page row, ordered by
// created_at descending and then ID descending.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

func (c Cursor) IsZero() bool { return c.CreatedAt.IsZero() || c.ID == "" }

// Page has one look-ahead cursor rather than a costly COUNT(*) total.
type Page struct {
	Plans   []Plan
	Next    Cursor
	HasMore bool
}

// Store is the plan persistence boundary.
type Store interface {
	List(ctx context.Context, tenantID string, options ListOptions) (Page, error)
}
