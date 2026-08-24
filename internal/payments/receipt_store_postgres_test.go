package payments

import "testing"

func TestReceiptPayloadMatchesOnlyFrozenVerifiedFacts(t *testing.T) {
	receipt := validReceiptEmail
	payload := []byte(`{
  "payment_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc",
  "customer_id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd",
  "plan_name":"Weekly access",
  "reference":"pay_12345678901234567890123456789012",
  "amount_minor":250000,
  "currency":"NGN",
  "starts_at":"2026-08-24T10:00:00Z",
  "expires_at":"2026-08-31T10:00:00Z"
}`)
	if !receiptPayloadMatches(payload, "cccccccc-cccc-4ccc-8ccc-cccccccccccc", "dddddddd-dddd-4ddd-8ddd-dddddddddddd", receipt) {
		t.Fatal("matching verified receipt facts were rejected")
	}
	payload = []byte(`{"payment_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","customer_id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd","plan_name":"Weekly access","reference":"pay_12345678901234567890123456789012","amount_minor":1,"currency":"NGN","starts_at":"2026-08-24T10:00:00Z","expires_at":"2026-08-31T10:00:00Z"}`)
	if receiptPayloadMatches(payload, "cccccccc-cccc-4ccc-8ccc-cccccccccccc", "dddddddd-dddd-4ddd-8ddd-dddddddddddd", receipt) {
		t.Fatal("payload with a stale amount was accepted")
	}
}
