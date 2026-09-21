package subscriptions

import (
	"context"
	"time"
)

// ExpiryStore reconciles elapsed active entitlements. Network authorization
// independently checks expires_at, so this job is a lifecycle/UI repair rather
// than the sole access-control boundary.
type ExpiryStore interface {
	ExpireDue(context.Context, string, time.Time) (int, error)
}

type ExpiryProcessor struct {
	store ExpiryStore
	now   func() time.Time
}

func NewExpiryProcessor(store ExpiryStore) (*ExpiryProcessor, error) {
	if store == nil {
		return nil, ErrUnavailable
	}
	return &ExpiryProcessor{store: store, now: time.Now}, nil
}

func (p *ExpiryProcessor) Process(ctx context.Context, tenantID string) (int, error) {
	return p.store.ExpireDue(ctx, tenantID, p.now().UTC())
}
