package subscriptions

import "testing"

// §20 — the full transition matrix, asserted exhaustively rather than by
// sampling. A new status added without updating the table fails here.
func TestTransitionMatrix(t *testing.T) {
	legal := map[Status]map[Status]bool{
		StatusPending:   {StatusActive: true, StatusCancelled: true},
		StatusActive:    {StatusSuspended: true, StatusExpired: true, StatusCancelled: true},
		StatusSuspended: {StatusActive: true, StatusExpired: true, StatusCancelled: true},
		StatusExpired:   {StatusActive: true, StatusCancelled: true},
		StatusCancelled: {},
	}

	for _, from := range AllStatuses() {
		for _, to := range AllStatuses() {
			if from == to {
				continue
			}
			want := legal[from][to]
			if got := CanTransition(from, to); got != want {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestTransition_SameStateIsIdempotent(t *testing.T) {
	for _, s := range AllStatuses() {
		if err := Transition(s, s); err != nil {
			t.Errorf("Transition(%s, %s) = %v, want nil (idempotent replay)", s, s, err)
		}
	}
}

func TestTransition_IllegalReturnsTypedError(t *testing.T) {
	err := Transition(StatusCancelled, StatusActive)
	if err == nil {
		t.Fatal("CANCELLED is terminal; reactivation must be rejected")
	}
	if _, ok := err.(*InvalidTransitionError); !ok {
		t.Fatalf("error type = %T, want *InvalidTransitionError", err)
	}
}

func TestTransition_UnknownStatusRejected(t *testing.T) {
	if err := Transition("ZOMBIE", StatusActive); err == nil {
		t.Error("unknown source status must be rejected")
	}
	if err := Transition(StatusActive, "ZOMBIE"); err == nil {
		t.Error("unknown target status must be rejected")
	}
}

// A cancelled subscription must never come back. Reactivation is a new
// subscription, not a state change — otherwise refund and billing history
// become ambiguous.
func TestCancelledIsTerminal(t *testing.T) {
	if !IsTerminal(StatusCancelled) {
		t.Fatal("CANCELLED must be terminal")
	}
	for _, to := range AllStatuses() {
		if to == StatusCancelled {
			continue
		}
		if CanTransition(StatusCancelled, to) {
			t.Errorf("CANCELLED -> %s must be illegal", to)
		}
	}
}

func TestGrantsAccess_OnlyActive(t *testing.T) {
	for _, s := range AllStatuses() {
		want := s == StatusActive
		if got := GrantsAccess(s); got != want {
			t.Errorf("GrantsAccess(%s) = %v, want %v", s, got, want)
		}
	}
}

// §20 — every entitlement-reducing transition must be detected, because each
// one has to enqueue CoA enforcement in the same transaction.
func TestReducesEntitlement(t *testing.T) {
	reducing := []struct{ from, to Status }{
		{StatusActive, StatusSuspended},
		{StatusActive, StatusExpired},
		{StatusActive, StatusCancelled},
	}
	for _, c := range reducing {
		if !ReducesEntitlement(c.from, c.to) {
			t.Errorf("%s -> %s must be flagged as entitlement-reducing (needs CoA)", c.from, c.to)
		}
	}

	nonReducing := []struct{ from, to Status }{
		{StatusPending, StatusActive},
		{StatusExpired, StatusActive},
		{StatusSuspended, StatusActive},
		{StatusPending, StatusCancelled}, // never had access
		{StatusExpired, StatusCancelled},
	}
	for _, c := range nonReducing {
		if ReducesEntitlement(c.from, c.to) {
			t.Errorf("%s -> %s must not be flagged as entitlement-reducing", c.from, c.to)
		}
	}
}

// Exhaustive cross-check: the flag must be exactly "was active, now isn't".
func TestReducesEntitlement_MatchesAccessDefinition(t *testing.T) {
	for _, from := range AllStatuses() {
		for _, to := range AllStatuses() {
			want := GrantsAccess(from) && !GrantsAccess(to)
			if got := ReducesEntitlement(from, to); got != want {
				t.Errorf("ReducesEntitlement(%s,%s) = %v, want %v", from, to, got, want)
			}
		}
	}
}

// §21A.2 — becoming ACTIVE requires a counter row to exist, or accounting has
// nowhere to land and quota_apply raises.
func TestRequiresQuotaPeriod(t *testing.T) {
	if !RequiresQuotaPeriod(StatusActive) {
		t.Error("ACTIVE must require a usage_counters period")
	}
	for _, s := range AllStatuses() {
		if s == StatusActive {
			continue
		}
		if RequiresQuotaPeriod(s) {
			t.Errorf("%s must not require a quota period", s)
		}
	}
}

func TestIsValid(t *testing.T) {
	for _, s := range AllStatuses() {
		if !IsValid(s) {
			t.Errorf("IsValid(%s) = false", s)
		}
	}
	for _, s := range []Status{"", "active", "ACTIVE ", "DELETED"} {
		if IsValid(s) {
			t.Errorf("IsValid(%q) = true, want false", s)
		}
	}
}
