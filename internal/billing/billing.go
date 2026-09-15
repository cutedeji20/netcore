// Package billing owns staff-facing revenue read models. It deliberately
// separates safe transaction listing from gateway callbacks, reconciliation,
// invoice issuance, and ledger writes.
package billing

import (
	"context"
	"errors"
	"time"

	"github.com/netcore-isp/netcore/internal/auth"
)

var (
	ErrInvalidPage  = errors.New("billing: invalid page request")
	ErrUnavailable  = errors.New("billing: transaction data unavailable")
	ErrInvalidClear = errors.New("billing: invalid payment-attempt clear request")
	ErrStepUpFailed = errors.New("billing: step-up verification failed")
)

// Source distinguishes invoice records from payment records in the unified
// billing list.
type Source string

const (
	SourcePayment Source = "PAYMENT"
	SourceInvoice Source = "INVOICE"
)

func IsValidSource(source Source) bool {
	return source == SourcePayment || source == SourceInvoice
}

// Transaction is a staff-safe entry for the revenue operations table. Amounts
// are stored as integer minor units and converted to strings only at the HTTP
// boundary to preserve exact values in browser clients.
type Transaction struct {
	ID                string
	Source            Source
	Reference         string
	CustomerID        string
	CustomerNumber    string
	CustomerFirstName string
	CustomerLastName  string
	AmountMinor       int64
	Currency          string
	Status            string
	RecordedAt        time.Time
	DueAt             *time.Time
	VerifiedAt        *time.Time
}

// ListOptions is a bounded, keyset-paginated unified billing query. Source is
// optional and lets the UI narrow to payments or invoices without a second API.
type ListOptions struct {
	Limit  int
	Cursor Cursor
	Search string
	Source Source
}

// Cursor selects rows after the preceding page's final row, ordered by
// recorded_at, source, and ID, all descending.
type Cursor struct {
	RecordedAt time.Time
	Source     Source
	ID         string
}

func (c Cursor) IsZero() bool {
	return c.RecordedAt.IsZero() || c.Source == "" || c.ID == ""
}

// Page contains one look-ahead cursor rather than a full-table count.
type Page struct {
	Transactions []Transaction
	Next         Cursor
	HasMore      bool
}

// Store is the billing persistence boundary.
type Store interface {
	List(ctx context.Context, tenantID string, options ListOptions) (Page, error)
	Clear(ctx context.Context, tenantID string, actor MutationActor, request ClearRequest) (int, error)
}

type ClearScope string

const (
	ClearPending  ClearScope = "PENDING"
	ClearFailed   ClearScope = "FAILED"
	ClearSelected ClearScope = "SELECTED"
)

type ClearRequest struct {
	Scope      ClearScope
	PaymentIDs []string
}

type MutationActor struct {
	UserID    string
	IP        string
	UserAgent string
}

type StepUpVerifier interface {
	VerifyStepUp(context.Context, auth.StepUpInput) error
}

// Service ensures destructive-looking dashboard controls remain a reversible,
// MFA-gated operational action rather than a database delete.
type Service struct {
	store  Store
	stepUp StepUpVerifier
}

func NewService(store Store, stepUp StepUpVerifier) (*Service, error) {
	if store == nil || stepUp == nil {
		return nil, ErrUnavailable
	}
	return &Service{store: store, stepUp: stepUp}, nil
}

func (s *Service) Clear(ctx context.Context, principal auth.Principal, actor MutationActor, password, mfaCode string, request ClearRequest) (int, error) {
	if s == nil || s.store == nil || s.stepUp == nil || !validUUID(principal.TenantID) || !validUUID(principal.UserID) || !validClearRequest(request) {
		return 0, ErrInvalidClear
	}
	if err := s.stepUp.VerifyStepUp(ctx, auth.StepUpInput{Principal: principal, Password: password, MFACode: mfaCode}); err != nil {
		return 0, ErrStepUpFailed
	}
	actor.UserID = principal.UserID
	return s.store.Clear(ctx, principal.TenantID, actor, request)
}

func validClearRequest(request ClearRequest) bool {
	switch request.Scope {
	case ClearPending, ClearFailed:
		return len(request.PaymentIDs) == 0
	case ClearSelected:
		if len(request.PaymentIDs) < 1 || len(request.PaymentIDs) > 100 {
			return false
		}
		seen := make(map[string]struct{}, len(request.PaymentIDs))
		for _, id := range request.PaymentIDs {
			if !validUUID(id) {
				return false
			}
			if _, duplicate := seen[id]; duplicate {
				return false
			}
			seen[id] = struct{}{}
		}
		return true
	default:
		return false
	}
}
