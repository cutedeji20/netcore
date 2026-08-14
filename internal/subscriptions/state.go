// Package subscriptions owns the subscription lifecycle.
//
// Spec: BUILD.md §20.
//
// The transition table lives here and only here. §20 requires transitions be
// explicit and enforced in one place rather than scattered across handlers;
// a status assignment anywhere else in the codebase is a bug.
package subscriptions

import (
	"fmt"
	"sort"
)

// Status is a subscription's lifecycle state.
type Status string

const (
	StatusPending   Status = "PENDING"
	StatusActive    Status = "ACTIVE"
	StatusSuspended Status = "SUSPENDED"
	StatusExpired   Status = "EXPIRED"
	StatusCancelled Status = "CANCELLED"
)

// AllStatuses returns every valid status, sorted, for exhaustive testing.
func AllStatuses() []Status {
	s := []Status{StatusPending, StatusActive, StatusSuspended, StatusExpired, StatusCancelled}
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s
}

// transitions is the authoritative table from §20.
var transitions = map[Status][]Status{
	StatusPending:   {StatusActive, StatusCancelled},
	StatusActive:    {StatusSuspended, StatusExpired, StatusCancelled},
	StatusSuspended: {StatusActive, StatusExpired, StatusCancelled},
	StatusExpired:   {StatusActive, StatusCancelled}, // renewal reactivates
	StatusCancelled: {},                              // terminal
}

// InvalidTransitionError describes a rejected state change.
type InvalidTransitionError struct{ From, To Status }

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("subscriptions: illegal transition %s -> %s", e.From, e.To)
}

// CanTransition reports whether from -> to is permitted.
func CanTransition(from, to Status) bool {
	for _, allowed := range transitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Transition validates a state change, returning an error if illegal.
func Transition(from, to Status) error {
	if !IsValid(from) {
		return fmt.Errorf("subscriptions: unknown source status %q", from)
	}
	if !IsValid(to) {
		return fmt.Errorf("subscriptions: unknown target status %q", to)
	}
	if from == to {
		// Idempotent no-op. Callers re-applying an event must not error, but
		// they also must not write a duplicate subscription_events row.
		return nil
	}
	if !CanTransition(from, to) {
		return &InvalidTransitionError{From: from, To: to}
	}
	return nil
}

// IsValid reports whether s is a known status.
func IsValid(s Status) bool {
	_, ok := transitions[s]
	return ok
}

// IsTerminal reports whether no transition out of s exists.
func IsTerminal(s Status) bool { return len(transitions[s]) == 0 }

// GrantsAccess reports whether a subscription in this state entitles the
// customer to network access. Only ACTIVE does.
func GrantsAccess(s Status) bool { return s == StatusActive }

// ReducesEntitlement reports whether a transition removes access the customer
// currently has.
//
// §20: every such transition must enqueue CoA/Disconnect enforcement in the
// SAME transaction as the status update. A subscription that is EXPIRED in
// Postgres while the customer still passes traffic is not expired — it is
// unbilled bandwidth.
func ReducesEntitlement(from, to Status) bool {
	return GrantsAccess(from) && !GrantsAccess(to)
}

// RequiresQuotaPeriod reports whether entering this state needs a
// usage_counters row to exist (§21A.2).
//
// Without one, quota_apply raises ErrNoCounter and real traffic has nowhere
// to be recorded.
func RequiresQuotaPeriod(s Status) bool { return s == StatusActive }
