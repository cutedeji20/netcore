package vouchers

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/security"
)

var (
	ErrRedeemDenied              = errors.New("vouchers: code cannot be redeemed")
	ErrVoucherNotFound           = errors.New("vouchers: code was not found")
	ErrVoucherAlreadyRedeemed    = errors.New("vouchers: code has already been redeemed")
	ErrVoucherExpired            = errors.New("vouchers: code has expired")
	ErrVoucherRevoked            = errors.New("vouchers: code has been revoked")
	ErrVoucherPlanUnavailable    = errors.New("vouchers: voucher plan is unavailable")
	ErrVoucherHotspotUnavailable = errors.New("vouchers: hotspot is unavailable")
)

// Redemption is the durable outcome of a voucher claim. A queued redemption
// deliberately has no active entitlement until its scheduled start time.
type Redemption struct {
	SubscriptionID string
	Queued         bool
	StartsAt       time.Time
	ExpiresAt      time.Time
}

type RedemptionStore interface {
	RedeemForDevice(context.Context, string, [32]byte, string, string) (Redemption, error)
	ActivateDue(context.Context, string, time.Time) (int, error)
}

// ActivationProcessor advances queued, already-paid voucher subscriptions.
// It is intentionally separate from HTTP so a browser refresh cannot decide
// when a queued entitlement begins.
type ActivationProcessor struct {
	store RedemptionStore
	now   func() time.Time
}

func NewActivationProcessor(store RedemptionStore) (*ActivationProcessor, error) {
	if store == nil {
		return nil, ErrRedeemDenied
	}
	return &ActivationProcessor{store: store, now: time.Now}, nil
}

func (p *ActivationProcessor) Process(ctx context.Context, tenantID string) (int, error) {
	if p == nil || p.store == nil || !validUUID(tenantID) {
		return 0, ErrRedeemDenied
	}
	return p.store.ActivateDue(ctx, tenantID, p.now().UTC())
}

type Redeemer struct{ store RedemptionStore }

func NewRedeemer(store RedemptionStore) (*Redeemer, error) {
	if store == nil {
		return nil, ErrRedeemDenied
	}
	return &Redeemer{store: store}, nil
}

func validBearerCode(code string) bool {
	normalized := normalizeCode(code)
	if !strings.HasPrefix(normalized, "NC") {
		return false
	}
	if len(normalized) == 14 {
		for _, c := range normalized[2:] {
			if !strings.ContainsRune(voucherAlphabet, c) {
				return false
			}
		}
		return true
	}
	// Preserve both voucher formats issued before the customer-entry format
	// was shortened. Those formats used RFC 4648 base32 characters.
	if len(normalized) != 20 && len(normalized) != 34 {
		return false
	}
	for _, c := range normalized[2:] {
		if !(c >= 'A' && c <= 'Z') && !(c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}

func (s *Redeemer) RedeemForDevice(ctx context.Context, tenantID, code, mac, nas string) (Redemption, error) {
	if s == nil || s.store == nil || !validUUID(tenantID) || !validBearerCode(code) {
		return Redemption{}, ErrRedeemDenied
	}
	normalizedMAC, ok := security.NormalizeMAC(mac)
	if !ok {
		return Redemption{}, ErrRedeemDenied
	}
	address, err := netip.ParseAddr(strings.TrimSpace(nas))
	if err != nil {
		return Redemption{}, ErrRedeemDenied
	}
	return s.store.RedeemForDevice(ctx, tenantID, HashCode(code), normalizedMAC, address.String())
}
