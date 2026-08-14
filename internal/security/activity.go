package security

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidActivityPage = errors.New("security: invalid activity page request")
	ErrActivityUnavailable = errors.New("security: activity data unavailable")
)

// ActivityEvent is the staff-safe projection of an immutable audit record.
// It intentionally omits IP addresses, user agents, request IDs, metadata,
// actor IDs, and resource IDs.
type ActivityEvent struct {
	ID           string
	Action       string
	Actor        string
	ResourceType string
	CreatedAt    time.Time
}

// ActivityListOptions is a bounded, keyset-paginated audit activity request.
type ActivityListOptions struct {
	Limit  int
	Cursor ActivityCursor
	Search string
}

// ActivityCursor selects rows after the previous page's final row, ordered by
// creation time and ID, both descending.
type ActivityCursor struct {
	CreatedAt time.Time
	ID        string
}

func (c ActivityCursor) IsZero() bool {
	return c.CreatedAt.IsZero() || c.ID == ""
}

type ActivityPage struct {
	Events  []ActivityEvent
	Next    ActivityCursor
	HasMore bool
}

// ActivityStore is the security-center audit read persistence boundary.
type ActivityStore interface {
	ListActivity(ctx context.Context, tenantID string, options ActivityListOptions) (ActivityPage, error)
}
