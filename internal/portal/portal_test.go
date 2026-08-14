package portal

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/url"
	"testing"
	"time"
)

type memoryStore struct {
	record HandoffRecord
	err    error
}

func (s *memoryStore) IssueHandoff(_ context.Context, record HandoffRecord) error {
	s.record = record
	return s.err
}

func TestIssueCreatesDigestOnlyBoundedHandoff(t *testing.T) {
	store := &memoryStore{}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	handoff, err := service.Issue(
		context.Background(),
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
		"AA:BB:CC:DD:EE:FF",
		"10.10.0.1",
		"http://10.10.0.1/login",
	)

	if err != nil {
		t.Fatal(err)
	}
	redirect, err := url.Parse(handoff.RedirectURL)
	if err != nil {
		t.Fatal(err)
	}
	token := redirect.Query().Get("username")
	if redirect.Hostname() != "10.10.0.1" || len(token) != 43 || redirect.Query().Get("password") != "portal-handoff" || handoff.ExpiresAt != now.Add(HandoffTTL) {
		t.Fatalf("unexpected handoff: %+v", handoff)
	}
	if store.record.ClientMAC != "aabbccddeeff" || store.record.NASAddress != "10.10.0.1" || len(store.record.TokenHash) != sha256.Size {
		t.Fatalf("unexpected stored record: %+v", store.record)
	}
	expected := sha256.Sum256([]byte(token))
	if string(store.record.TokenHash) != string(expected[:]) {
		t.Fatal("stored digest does not match returned token")
	}
	if string(store.record.TokenHash) == token {
		t.Fatal("raw handoff token reached the store")
	}
}

func TestIssuePreservesNoActivePlanWithoutToken(t *testing.T) {
	store := &memoryStore{err: ErrNoActivePlan}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}

	handoff, err := service.Issue(
		context.Background(),
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
		"aabbccddeeff",
		"10.10.0.1",
		"http://10.10.0.1/login",
	)

	if !errors.Is(err, ErrNoActivePlan) || handoff.RedirectURL != "" {
		t.Fatalf("handoff=%+v err=%v", handoff, err)
	}
}

func TestIssueRejectsMalformedConnectionContext(t *testing.T) {
	service, err := NewService(&memoryStore{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Issue(
		context.Background(),
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
		"not-a-mac",
		"not-an-address",
		"http://10.10.0.1/login",
	)
	if !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("error = %v", err)
	}
}

func TestIssueRejectsForeignRouterLoginURL(t *testing.T) {
	service, err := NewService(&memoryStore{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Issue(
		context.Background(),
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
		"aabbccddeeff",
		"10.10.0.1",
		"https://attacker.example/login",
	)
	if !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("error = %v", err)
	}
}
