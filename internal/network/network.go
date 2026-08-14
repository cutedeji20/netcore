// Package network owns staff-facing network inventory read models. It exposes
// operational state while keeping management addresses, API endpoints, and
// secret-reference paths inside the protected backend.
package network

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidPage = errors.New("network: invalid page request")
	ErrUnavailable = errors.New("network: router data unavailable")
)

// RouterStatus is the router lifecycle state stored in PostgreSQL.
type RouterStatus string

const (
	RouterStatusProvisioning RouterStatus = "PROVISIONING"
	RouterStatusOnline       RouterStatus = "ONLINE"
	RouterStatusOffline      RouterStatus = "OFFLINE"
	RouterStatusRetired      RouterStatus = "RETIRED"
)

func IsValidRouterStatus(status RouterStatus) bool {
	switch status {
	case RouterStatusProvisioning, RouterStatusOnline, RouterStatusOffline, RouterStatusRetired:
		return true
	default:
		return false
	}
}

// Router is a staff-safe network inventory record.
type Router struct {
	ID         string
	Name       string
	SiteName   string
	Status     RouterStatus
	AAAStatus  string
	LastSeenAt *time.Time
}

// ListOptions is a bounded alphabetic keyset-paginated router query. Router
// names are unique within a tenant, making the name an unambiguous cursor.
type ListOptions struct {
	Limit  int
	Cursor Cursor
	Search string
	Status RouterStatus
}

type Cursor struct{ Name string }

func (c Cursor) IsZero() bool { return c.Name == "" }

type Page struct {
	Routers []Router
	Next    Cursor
	HasMore bool
}

// Store is the router persistence boundary.
type Store interface {
	List(ctx context.Context, tenantID string, options ListOptions) (Page, error)
}
