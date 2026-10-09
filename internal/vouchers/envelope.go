package vouchers

import (
	"context"
	"errors"

	"github.com/netcore-isp/netcore/pkg/crypto/envelope"
)

var ErrCodeUnavailable = errors.New("vouchers: code unavailable")

type CodeEnvelope = envelope.Record

func codeAAD(tenantID, voucherID string) []byte {
	return []byte("netcore/vouchers/v1/" + tenantID + "/" + voucherID)
}

func SealCode(ctx context.Context, wrapper envelope.KeyWrapper, tenantID, voucherID, code string) (CodeEnvelope, error) {
	if tenantID == "" || voucherID == "" || code == "" {
		return CodeEnvelope{}, ErrCodeUnavailable
	}
	record, err := envelope.Seal(ctx, wrapper, codeAAD(tenantID, voucherID), []byte(code))
	if err != nil {
		return CodeEnvelope{}, ErrCodeUnavailable
	}
	return record, nil
}

func OpenCode(ctx context.Context, wrapper envelope.KeyWrapper, tenantID, voucherID string, record CodeEnvelope) (string, error) {
	if tenantID == "" || voucherID == "" {
		return "", ErrCodeUnavailable
	}
	plain, err := envelope.Open(ctx, wrapper, codeAAD(tenantID, voucherID), record)
	if err != nil {
		return "", ErrCodeUnavailable
	}
	return string(plain), nil
}
