package vouchers

import (
	"context"
	"testing"

	"github.com/netcore-isp/netcore/pkg/crypto/envelope"
)

type voucherTestWrapper struct{}

func (voucherTestWrapper) Wrap(_ context.Context, key []byte) (envelope.WrappedKey, error) {
	return envelope.WrappedKey{Ciphertext: append([]byte(nil), key...), KeyID: "https://vault.test/keys/voucher/1"}, nil
}
func (voucherTestWrapper) Unwrap(_ context.Context, key []byte, _ string) ([]byte, error) {
	return append([]byte(nil), key...), nil
}

func TestVoucherEnvelopeBindsTenantAndVoucher(t *testing.T) {
	ctx := context.Background()
	const tenant = "11111111-1111-4111-8111-111111111111"
	const id = "22222222-2222-4222-8222-222222222222"
	record, err := SealCode(ctx, voucherTestWrapper{}, tenant, id, "NC-SECRET")
	if err != nil {
		t.Fatal(err)
	}
	code, err := OpenCode(ctx, voucherTestWrapper{}, tenant, id, record)
	if err != nil || code != "NC-SECRET" {
		t.Fatalf("code=%q err=%v", code, err)
	}
	if _, err := OpenCode(ctx, voucherTestWrapper{}, "other-tenant", id, record); err == nil {
		t.Fatal("foreign tenant opened code")
	}
	if _, err := OpenCode(ctx, voucherTestWrapper{}, tenant, "other-voucher", record); err == nil {
		t.Fatal("foreign voucher opened code")
	}
}
