package payments

import (
	"testing"
	"time"
)

func TestNewReceiptEventUsesOnePaymentScopedIdentifier(t *testing.T) {
	startsAt := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	expiresAt := startsAt.Add(7 * 24 * time.Hour)
	first, err := NewReceiptEvent(
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		"Weekly access",
		"pay_12345678901234567890123456789012",
		250000,
		"NGN",
		startsAt,
		expiresAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewReceiptEvent(
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		"Weekly access",
		"pay_12345678901234567890123456789012",
		250000,
		"NGN",
		startsAt,
		expiresAt,
	)
	if err != nil || first.EventID != second.EventID || first.Reference != "pay_12345678901234567890123456789012" {
		t.Fatalf("first=%+v second=%+v err=%v", first, second, err)
	}
}

func TestNewReceiptEventRejectsUnsafeReceiptFacts(t *testing.T) {
	if _, err := NewReceiptEvent("bad", "customer", "", "wrong", 0, "N", time.Time{}, time.Time{}); err == nil {
		t.Fatal("unsafe receipt event accepted")
	}
}
