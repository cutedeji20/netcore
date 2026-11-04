package vouchers

import (
	"context"
	"errors"
	"testing"
	"time"
)

type redemptionStoreStub struct {
	calls    int
	hash     [32]byte
	mac, nas string
}

func (s *redemptionStoreStub) RedeemForDevice(_ context.Context, _ string, hash [32]byte, mac, nas string) (string, error) {
	s.calls++
	s.hash, s.mac, s.nas = hash, mac, nas
	return "subscription", nil
}

func (s *redemptionStoreStub) RedeemForCustomerDevice(_ context.Context, _, _ string, hash [32]byte, mac, nas string) (Redemption, error) {
	s.calls++
	s.hash, s.mac, s.nas = hash, mac, nas
	return Redemption{SubscriptionID: "customer-subscription"}, nil
}

func (s *redemptionStoreStub) ActivateDue(_ context.Context, _ string, _ time.Time) (int, error) {
	return 0, nil
}

func TestRedeemForDeviceValidatesCodeMACAndNAS(t *testing.T) {
	store := &redemptionStoreStub{}
	s, _ := NewRedeemer(store)
	for _, code := range []string{"bad", "NC-INVALID"} {
		if _, err := s.RedeemForDevice(context.Background(), voucherTestTenantID, code, "aa:bb:cc:dd:ee:ff", "192.0.2.1"); !errors.Is(err, ErrRedeemDenied) {
			t.Fatalf("code=%q err=%v", code, err)
		}
	}
	if store.calls != 0 {
		t.Fatal("invalid codes reached database")
	}
	code, err := NewCode()
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.RedeemForDevice(context.Background(), voucherTestTenantID, code, "AA:BB:CC:DD:EE:FF", "192.0.2.1")
	if err != nil || id != "subscription" || store.mac != "aabbccddeeff" || store.nas != "192.0.2.1" || store.hash != HashCode(code) {
		t.Fatalf("id=%q err=%v store=%+v", id, err, store)
	}
	legacy := "NC-ABCDEFGH-JKLMNPQR-STUVWXYZ-23456723"
	if _, err := s.RedeemForDevice(context.Background(), voucherTestTenantID, legacy, "AA:BB:CC:DD:EE:FF", "192.0.2.1"); err != nil || store.hash != HashCode(legacy) {
		t.Fatalf("previously issued code rejected: %v", err)
	}
}

func TestRedeemForCustomerDeviceNormalizesOnlyAtBoundary(t *testing.T) {
	store := &redemptionStoreStub{}
	redeemer, _ := NewRedeemer(store)
	code, err := NewCode()
	if err != nil {
		t.Fatal(err)
	}
	claim, err := redeemer.RedeemForCustomerDevice(context.Background(), voucherTestTenantID, "22222222-2222-4222-8222-222222222222", code, "AA:BB:CC:DD:EE:FF", "192.0.2.1")
	if err != nil || claim.SubscriptionID != "customer-subscription" || store.mac != "aabbccddeeff" || store.hash != HashCode(code) {
		t.Fatalf("claim=%+v err=%v store=%+v", claim, err, store)
	}
}
