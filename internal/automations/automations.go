// Package automations owns staff-facing workflow inventory read models. It
// does not execute workflows or expose implementation configuration.
package automations

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidPage = errors.New("automations: invalid page request")
	ErrUnavailable = errors.New("automations: workflow data unavailable")
)

type Status string

const (
	StatusDraft  Status = "DRAFT"
	StatusReady  Status = "READY"
	StatusPaused Status = "PAUSED"
)

func IsValidStatus(status Status) bool {
	switch status {
	case StatusDraft, StatusReady, StatusPaused:
		return true
	default:
		return false
	}
}

// Workflow is the staff-safe workflow inventory projection. It excludes
// execution payloads, provider credentials, and any secret references.
type Workflow struct {
	ID                 string
	Name               string
	TriggerDescription string
	Status             Status
	NextRunAt          *time.Time
	Owner              string
	UpdatedAt          time.Time
}

type ListOptions struct {
	Limit  int
	Cursor Cursor
	Search string
	Status Status
}

// Cursor selects records after the prior page's final row, ordered by updated
// time and ID, both descending.
type Cursor struct {
	UpdatedAt time.Time
	ID        string
}

func (c Cursor) IsZero() bool {
	return c.UpdatedAt.IsZero() || c.ID == ""
}

type Page struct {
	Workflows []Workflow
	Next      Cursor
	HasMore   bool
}

// Store is the automation inventory persistence boundary.
type Store interface {
	List(ctx context.Context, tenantID string, options ListOptions) (Page, error)
}
