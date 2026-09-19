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
	ErrNotFound    = errors.New("network: router not found")
	ErrConflict    = errors.New("network: lifecycle state changed")
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
	ID          string
	Name        string
	SiteName    string
	Status      RouterStatus
	AAAStatus   string
	AAAVerified bool
	LastSeenAt  *time.Time
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

// MutationActor identifies the staff member responsible for a network change.
type MutationActor struct {
	UserID    string
	IP        string
	UserAgent string
}

// TetheringPolicy is the tenant's desired HotSpot TTL enforcement policy.
// Rendering/applying RouterOS rules remains an explicit operator action.
type TetheringPolicy struct {
	Enabled           bool
	ExpectedClientTTL int
}

type TetheringStore interface {
	LoadTetheringPolicy(context.Context, string) (TetheringPolicy, error)
	SaveTetheringPolicy(context.Context, string, MutationActor, TetheringPolicy) error
}

// NASStatus is the lifecycle state accepted by the RADIUS policy.
type NASStatus string

const (
	NASStatusActive   NASStatus = "ACTIVE"
	NASStatusDisabled NASStatus = "DISABLED"
)

// AAAConfiguration is safe to display to authorised staff. It deliberately
// omits the shared secret, secret reference, and encrypted envelope bytes.
type AAAConfiguration struct {
	RouterID       string
	NASID          string
	NASIPAddress   string
	RadiusSourceIP string
	ShortName      string
	Status         NASStatus
	Version        int64
	Verified       bool
}

// LifecycleStore is the network mutation boundary used by MFA-gated routes.
type LifecycleStore interface {
	CreateRouter(ctx context.Context, tenantID string, actor MutationActor, input RouterCreateInput) (AAAConfiguration, error)
	LoadAAA(ctx context.Context, tenantID, routerID string) (AAAConfiguration, error)
	SaveCredential(ctx context.Context, actor MutationActor, record RadiusCredentialRecord) error
	MarkVerified(ctx context.Context, tenantID, routerID string, actor MutationActor) error
	SetNASStatus(ctx context.Context, tenantID, routerID string, actor MutationActor, status NASStatus) error
}
