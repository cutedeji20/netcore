package portal

import (
	"context"
	"errors"
	"strings"
)

var (
	ErrCatalogueUnavailable = errors.New("portal: catalogue unavailable")
	ErrCatalogueNotFound    = errors.New("portal: configured catalogue tenant was not found")
)

// PublicPlan is the intentionally small, safe plan representation used by
// visitors before they sign in. It omits tenant identity, publication state,
// audit timestamps, internal quota mechanics, and subscriber counts.
type PublicPlan struct {
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
}

// CatalogueStore resolves only active tenants and returns only plans that are
// public and sellable according to the persistence boundary.
type CatalogueStore interface {
	ResolveTenant(ctx context.Context, slug string) (tenantID string, ok bool, err error)
	ListPublishedPlans(ctx context.Context, tenantID string) ([]PublicPlan, error)
}

// CatalogueService binds the public read model to a deployment-selected
// tenant. It has no browser-controlled tenant parameter by design.
type CatalogueService struct {
	store      CatalogueStore
	tenantSlug string
}

func NewCatalogueService(store CatalogueStore, tenantSlug string) (*CatalogueService, error) {
	if store == nil {
		return nil, errors.New("portal: catalogue store is required")
	}
	tenantSlug = strings.ToLower(strings.TrimSpace(tenantSlug))
	if tenantSlug == "" {
		return nil, errors.New("portal: catalogue tenant slug is required")
	}
	return &CatalogueService{store: store, tenantSlug: tenantSlug}, nil
}

func (s *CatalogueService) PublishedPlans(ctx context.Context) ([]PublicPlan, error) {
	if s == nil || s.store == nil {
		return nil, ErrCatalogueUnavailable
	}
	tenantID, found, err := s.store.ResolveTenant(ctx, s.tenantSlug)
	if err != nil {
		return nil, ErrCatalogueUnavailable
	}
	if !found || tenantID == "" {
		return nil, ErrCatalogueNotFound
	}
	plans, err := s.store.ListPublishedPlans(ctx, tenantID)
	if err != nil {
		return nil, ErrCatalogueUnavailable
	}
	return plans, nil
}
