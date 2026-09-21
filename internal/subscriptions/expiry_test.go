package subscriptions

import (
	"context"
	"testing"
	"time"
)

type expiryMemoryStore struct {
	tenant string
	at     time.Time
	count  int
	err    error
}

func (s *expiryMemoryStore) ExpireDue(_ context.Context, tenant string, at time.Time) (int, error) {
	s.tenant, s.at = tenant, at
	return s.count, s.err
}

func TestExpiryProcessorReconcilesDueEntitlements(t *testing.T) {
	store := &expiryMemoryStore{count: 2}
	p, err := NewExpiryProcessor(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.FixedZone("WAT", 3600))
	p.now = func() time.Time { return now }
	count, err := p.Process(context.Background(), subscriptionTestTenantID)
	if err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if store.tenant != subscriptionTestTenantID || !store.at.Equal(now.UTC()) {
		t.Fatalf("store=%+v", store)
	}
}

func TestExpiryProcessorRequiresStore(t *testing.T) {
	if _, err := NewExpiryProcessor(nil); err == nil {
		t.Fatal("missing store accepted")
	}
}
