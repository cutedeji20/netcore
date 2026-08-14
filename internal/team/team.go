// Package team owns staff-facing tenant membership read models. It reports
// access posture without exposing password data, session credentials, IP
// addresses, user agents, or MFA secret references.
package team

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidPage = errors.New("team: invalid page request")
	ErrUnavailable = errors.New("team: member data unavailable")
)

// Member is a staff-safe tenant user and access posture representation.
type Member struct {
	ID         string
	Email      string
	Phone      string
	Status     string
	Roles      []string
	MFAStatus  string
	LastSeenAt *time.Time
	CreatedAt  time.Time
}

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
	Members []Member
	Next    Cursor
	HasMore bool
}

// Store is the team membership persistence boundary.
type Store interface {
	List(ctx context.Context, tenantID string, options ListOptions) (Page, error)
}
