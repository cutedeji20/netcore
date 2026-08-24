// Package plans owns the sellable service catalogue. Subscription lifecycle
// reads plan terms but plan management remains the authoritative boundary for
// prices, speeds, quotas, and access limits.
package plans

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidPage  = errors.New("plans: invalid page request")
	ErrUnavailable  = errors.New("plans: plan data unavailable")
	ErrInvalidInput = errors.New("plans: invalid plan input")
	ErrNotFound     = errors.New("plans: plan not found")
	ErrTermsLocked  = errors.New("plans: plan terms are locked by subscriptions")
)

// Status is the publication state of a plan.
type Status string

const (
	StatusActive  Status = "ACTIVE"
	StatusRetired Status = "RETIRED"
)

func IsValidStatus(status Status) bool {
	return status == StatusActive || status == StatusRetired
}

// Plan is the staff-safe plan catalogue read model. Money remains in integer
// minor units all the way to the HTTP response boundary.
type Plan struct {
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
	QuotaBytes            *int64
	QuotaResetPolicy      string
	Status                Status
	ActiveSubscriptions   int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// ListOptions is a bounded, keyset-paginated catalogue query. An empty Status
// includes both published and retired plans.
type ListOptions struct {
	Limit  int
	Cursor Cursor
	Search string
	Status Status
}

// Cursor selects rows after the final preceding page row, ordered by
// created_at descending and then ID descending.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

func (c Cursor) IsZero() bool { return c.CreatedAt.IsZero() || c.ID == "" }

// Page has one look-ahead cursor rather than a costly COUNT(*) total.
type Page struct {
	Plans   []Plan
	Next    Cursor
	HasMore bool
}

// WriteInput contains the commercial terms available in the first plan
// management release. It deliberately excludes advanced burst/throttle tuning:
// a plan with a quota disconnects at exhaustion until that separate policy UI
// is delivered, rather than accidentally granting unrestricted bandwidth.
type WriteInput struct {
	Name                  string
	Description           string
	PriceMinor            int64
	Currency              string
	DurationSeconds       int64
	DownloadBPS           int64
	UploadBPS             int64
	MaxDevices            int
	MaxConcurrentSessions int
	QuotaBytes            *int64
	QuotaResetPolicy      string
	Status                Status
}

// MutationActor identifies the authorised staff member in the immutable audit
// record without putting browser-controlled identity data into the plan input.
type MutationActor struct {
	UserID    string
	IP        string
	UserAgent string
}

func (in *WriteInput) NormalizeAndValidate() error {
	if in == nil {
		return ErrInvalidInput
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	in.QuotaResetPolicy = strings.ToUpper(strings.TrimSpace(in.QuotaResetPolicy))
	in.Status = Status(strings.ToUpper(strings.TrimSpace(string(in.Status))))
	if in.Status == "" {
		in.Status = StatusActive
	}
	if in.QuotaResetPolicy == "" {
		in.QuotaResetPolicy = "NONE"
	}
	if in.Name == "" || len(in.Name) > 120 || len(in.Description) > 1000 || !validCurrency(in.Currency) ||
		in.PriceMinor < 0 || in.DurationSeconds < 60 || in.DurationSeconds > 5*365*24*60*60 ||
		in.DownloadBPS <= 0 || in.UploadBPS <= 0 || in.MaxDevices < 1 || in.MaxDevices > 100 ||
		in.MaxConcurrentSessions < 1 || in.MaxConcurrentSessions > in.MaxDevices || !IsValidStatus(in.Status) {
		return ErrInvalidInput
	}
	if in.QuotaBytes == nil {
		if in.QuotaResetPolicy != "NONE" {
			return ErrInvalidInput
		}
		return nil
	}
	if *in.QuotaBytes <= 0 || !validQuotaResetPolicy(in.QuotaResetPolicy) {
		return ErrInvalidInput
	}
	return nil
}

func validCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

func validQuotaResetPolicy(value string) bool {
	switch value {
	case "NONE", "PER_SUBSCRIPTION", "DAILY", "MONTHLY":
		return true
	default:
		return false
	}
}

// Store is the plan persistence boundary.
type Store interface {
	List(ctx context.Context, tenantID string, options ListOptions) (Page, error)
	Create(ctx context.Context, tenantID string, actor MutationActor, input WriteInput) (Plan, error)
	Update(ctx context.Context, tenantID, planID string, actor MutationActor, input WriteInput) (Plan, error)
}
