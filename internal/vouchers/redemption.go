package vouchers

import (
	"context"
	"errors"
	"net/netip"
	"strings"

	"github.com/netcore-isp/netcore/internal/security"
)

var ErrRedeemDenied = errors.New("vouchers: code cannot be redeemed")

type RedemptionStore interface {
	RedeemForDevice(context.Context, string, [32]byte, string, string) (string, error)
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
	if (len(normalized) != 20 && len(normalized) != 34) || !strings.HasPrefix(normalized, "NC") {
		return false
	}
	for _, c := range normalized[2:] {
		if !(c >= 'A' && c <= 'Z') && !(c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}

func (s *Redeemer) RedeemForDevice(ctx context.Context, tenantID, code, mac, nas string) (string, error) {
	if s == nil || s.store == nil || !validUUID(tenantID) || !validBearerCode(code) {
		return "", ErrRedeemDenied
	}
	normalizedMAC, ok := security.NormalizeMAC(mac)
	if !ok {
		return "", ErrRedeemDenied
	}
	address, err := netip.ParseAddr(strings.TrimSpace(nas))
	if err != nil {
		return "", ErrRedeemDenied
	}
	return s.store.RedeemForDevice(ctx, tenantID, HashCode(code), normalizedMAC, address.String())
}
