// Package portal owns captive-portal entitlement checks and the handoff nonce
// boundary. It never admits a device itself; RouterOS and FreeRADIUS remain
// the authorization path.
package portal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/security"
)

const HandoffTTL = 120 * time.Second

var (
	ErrNoActivePlan   = errors.New("portal: no active plan")
	ErrInvalidContext = errors.New("portal: invalid connection context")
	ErrUnavailable    = errors.New("portal: handoff unavailable")
)

// HandoffRecord carries only the digest to persistence. The raw token is
// returned once to the browser for the RouterOS login redirect and must never
// be logged or written to a database.
type HandoffRecord struct {
	TenantID   string
	UserID     string
	ClientMAC  string
	NASAddress string
	TokenHash  []byte
	ExpiresAt  time.Time
}

// HandoffStore atomically verifies entitlement, resolves the registered NAS,
// and persists the one-time nonce digest.
type HandoffStore interface {
	IssueHandoff(ctx context.Context, record HandoffRecord) error
}

// Service generates opaque handoff material. The store owns active-plan and
// NAS registration checks because both must run within one tenant transaction.
type Service struct {
	store HandoffStore
	now   func() time.Time
}

func NewService(store HandoffStore) (*Service, error) {
	if store == nil {
		return nil, errors.New("portal: handoff store is required")
	}
	return &Service{store: store, now: time.Now}, nil
}

// Handoff is shown once to the client and is valid only for the RouterOS
// handoff. It is not a browser session, API key, or general bearer token.
type Handoff struct {
	RedirectURL string
	ExpiresAt   time.Time
}

// Issue accepts RouterOS-provided connection context only as a binding
// candidate. It is not authorization: the final FreeRADIUS consume operation
// matches the nonce to the actual NAS and client MAC in the RADIUS request.
func (s *Service) Issue(ctx context.Context, tenantID, userID, clientMAC, nasAddress, hotspotLoginURL string) (Handoff, error) {
	if s == nil || s.store == nil {
		return Handoff{}, ErrUnavailable
	}
	if !validUUID(tenantID) || !validUUID(userID) {
		return Handoff{}, ErrInvalidContext
	}
	normalizedMAC, ok := security.NormalizeMAC(clientMAC)
	if !ok {
		return Handoff{}, ErrInvalidContext
	}
	nas, err := netip.ParseAddr(strings.TrimSpace(nasAddress))
	if err != nil {
		return Handoff{}, ErrInvalidContext
	}
	redirectBase, err := routerOSLoginURL(hotspotLoginURL, nas)
	if err != nil {
		return Handoff{}, ErrInvalidContext
	}

	token, digest, err := newToken()
	if err != nil {
		return Handoff{}, fmt.Errorf("%w: generate handoff token", ErrUnavailable)
	}
	expiresAt := s.now().UTC().Add(HandoffTTL)
	if err := s.store.IssueHandoff(ctx, HandoffRecord{
		TenantID:   tenantID,
		UserID:     userID,
		ClientMAC:  normalizedMAC,
		NASAddress: nas.String(),
		TokenHash:  digest,
		ExpiresAt:  expiresAt,
	}); err != nil {
		return Handoff{}, err
	}
	query := redirectBase.Query()
	query.Set("username", token)
	// RouterOS forwards User-Name to RADIUS for this narrowly scoped
	// handoff. The password is deliberately not a credential in this branch.
	query.Set("password", "portal-handoff")
	redirectBase.RawQuery = query.Encode()
	return Handoff{RedirectURL: redirectBase.String(), ExpiresAt: expiresAt}, nil
}

func routerOSLoginURL(raw string, nas netip.Addr) (*url.URL, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || !parsed.IsAbs() || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, ErrInvalidContext
	}
	host, err := netip.ParseAddr(parsed.Hostname())
	if err != nil || host != nas {
		return nil, ErrInvalidContext
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, ErrInvalidContext
		}
	}
	parsed.Fragment = ""
	return parsed, nil
}

func newToken() (string, []byte, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(value[:])
	digest := sha256.Sum256([]byte(token))
	return token, digest[:], nil
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if value[i] != '-' {
				return false
			}
			continue
		}
		if !((value[i] >= '0' && value[i] <= '9') || (value[i] >= 'a' && value[i] <= 'f') || (value[i] >= 'A' && value[i] <= 'F')) {
			return false
		}
	}
	return true
}
