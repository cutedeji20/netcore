package vouchers

import (
	"context"
	"errors"
	"testing"

	"github.com/netcore-isp/netcore/pkg/crypto/envelope"
)

type issuingStoreStub struct {
	saved int
	codes []sealedCode
}

func (s *issuingStoreStub) SaveBatch(_ context.Context, _ string, request IssueRequest, id string, codes []sealedCode) (Batch, error) {
	s.saved++
	s.codes = codes
	return Batch{ID: id, Issued: int64(request.Quantity)}, nil
}
func (*issuingStoreStub) RevealBatch(context.Context, envelope.KeyWrapper, string, string, string) ([]IssuedCode, error) {
	return nil, nil
}

func TestIssueBatchEncryptsAndHashesAllCodes(t *testing.T) {
	store := &issuingStoreStub{}
	issuer, err := NewIssuer(store, voucherTestWrapper{}, "https://vault.test/keys/voucher/1")
	if err != nil {
		t.Fatal(err)
	}
	batch, err := issuer.IssueBatch(context.Background(), "11111111-1111-4111-8111-111111111111", IssueRequest{PlanID: "22222222-2222-4222-8222-222222222222", Quantity: 3, IssuerUserID: "33333333-3333-4333-8333-333333333333", Reason: "test"})
	if err != nil || store.saved != 1 || batch.Issued != 3 || len(store.codes) != 3 {
		t.Fatalf("batch=%+v err=%v", batch, err)
	}
	seen := map[string]bool{}
	for _, item := range store.codes {
		if seen[item.code] || item.hash != HashCode(item.code) || len(item.record.Ciphertext) == 0 {
			t.Fatal("invalid issued code")
		}
		seen[item.code] = true
	}
}

func TestIssueBatchFailsClosedWithoutConfiguredKey(t *testing.T) {
	store := &issuingStoreStub{}
	issuer, err := NewIssuer(store, unavailableVoucherWrapper{}, "https://vault.test/keys/voucher/1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = issuer.IssueBatch(context.Background(), "11111111-1111-4111-8111-111111111111", IssueRequest{PlanID: "22222222-2222-4222-8222-222222222222", Quantity: 1, IssuerUserID: "33333333-3333-4333-8333-333333333333", Reason: "test"})
	if !errors.Is(err, ErrCodeUnavailable) || store.saved != 0 {
		t.Fatalf("err=%v saved=%d", err, store.saved)
	}
}

type unavailableVoucherWrapper struct{}

func (unavailableVoucherWrapper) Wrap(context.Context, []byte) (envelope.WrappedKey, error) {
	return envelope.WrappedKey{}, errors.New("unavailable")
}
func (unavailableVoucherWrapper) Unwrap(context.Context, []byte, string) ([]byte, error) {
	return nil, errors.New("unavailable")
}
