// Package workspace owns the staff-safe tenant profile read model used by the
// workspace settings screen.
package workspace

import (
	"context"
	"errors"
	"time"
)

var ErrUnavailable = errors.New("workspace: settings data unavailable")

// Snapshot is a safe operational view of one workspace. It contains neither
// integration tokens, secret references, provider credentials, nor raw router
// management configuration.
type Snapshot struct {
	Name              string
	Slug              string
	Timezone          string
	Currency          string
	Status            string
	UpdatedAt         time.Time
	RegisteredRouters int
	ActiveTeamMembers int
}

// Store is the workspace settings persistence boundary.
type Store interface {
	Get(ctx context.Context, tenantID string) (Snapshot, error)
}
