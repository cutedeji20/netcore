// Package sessions owns the staff-facing access-session read model. It never
// returns RADIUS targeting keys, NAS credentials, or accounting identifiers to
// the browser.
package sessions

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidPage = errors.New("sessions: invalid page request")
	ErrUnavailable = errors.New("sessions: session data unavailable")
)

// Status is the lifecycle state of an access session.
type Status string

const (
	StatusActive  Status = "ACTIVE"
	StatusSuspect Status = "SUSPECT"
	StatusClosed  Status = "CLOSED"
)

func IsValidStatus(status Status) bool {
	return status == StatusActive || status == StatusSuspect || status == StatusClosed
}

// Session is the staff-safe session read model. All byte totals are decimal
// strings so an API consumer does not round a bigint through a JSON number.
type Session struct {
	ID                 string
	CustomerID         string
	CustomerNumber     string
	CustomerFirstName  string
	CustomerLastName   string
	PlanName           string
	RouterID           string
	RouterName         string
	IPAddress          string
	Status             Status
	StartedAt          time.Time
	LastInterimAt      time.Time
	SessionBytes       string
	UsageConsumedBytes string
	UsageQuotaBytes    string
}

// ListOptions is a bounded keyset-paginated session query. Empty Status
// includes all session states; the live UI explicitly asks for ACTIVE.
type ListOptions struct {
	Limit  int
	Cursor Cursor
	Search string
	Status Status
}

// Cursor selects rows after the final row from the preceding page, ordered by
// started_at descending and then ID descending.
type Cursor struct {
	StartedAt time.Time
	ID        string
}

func (c Cursor) IsZero() bool { return c.StartedAt.IsZero() || c.ID == "" }

// Page uses one look-ahead cursor rather than a full-table count.
type Page struct {
	Sessions []Session
	Next     Cursor
	HasMore  bool
}

// Store is the session persistence boundary.
type Store interface {
	List(ctx context.Context, tenantID string, options ListOptions) (Page, error)
}
