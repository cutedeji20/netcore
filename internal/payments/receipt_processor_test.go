package payments

import (
	"context"
	"errors"
	"testing"
	"time"
)

var validReceiptEmail = ReceiptEmail{
	EventID:     "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	To:          "customer@example.com",
	PlanName:    "Weekly access",
	Reference:   "pay_12345678901234567890123456789012",
	AmountMinor: 250000,
	Currency:    "NGN",
	StartsAt:    time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC),
	ExpiresAt:   time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC),
}

type memoryReceiptStore struct {
	next        ReceiptEmail
	found       bool
	err         error
	publishedID string
	failedID    string
}

func (s *memoryReceiptStore) ClaimReceipt(context.Context, int) (ReceiptEmail, bool, error) {
	return s.next, s.found, s.err
}

func (s *memoryReceiptStore) MarkReceiptPublished(_ context.Context, eventID string) error {
	s.publishedID = eventID
	return nil
}

func (s *memoryReceiptStore) MarkReceiptFailed(_ context.Context, receipt ReceiptEmail, _ error) error {
	s.failedID = receipt.EventID
	return nil
}

type recordingReceiptSender struct {
	sent ReceiptEmail
	err  error
}

func (s *recordingReceiptSender) SendPaymentReceipt(_ context.Context, receipt ReceiptEmail) error {
	s.sent = receipt
	return s.err
}

func TestReceiptProcessorPublishesOnlyAfterDelivery(t *testing.T) {
	store := &memoryReceiptStore{next: validReceiptEmail, found: true}
	sender := &recordingReceiptSender{}
	processor, err := NewReceiptProcessor(store, sender, 5)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := processor.ProcessOne(context.Background())
	if err != nil || !worked || store.publishedID != validReceiptEmail.EventID || sender.sent.Reference != validReceiptEmail.Reference {
		t.Fatalf("worked=%v err=%v published=%q sent=%+v", worked, err, store.publishedID, sender.sent)
	}
}

func TestReceiptProcessorRetriesDeliveryWithoutChangingPayment(t *testing.T) {
	store := &memoryReceiptStore{next: validReceiptEmail, found: true}
	processor, err := NewReceiptProcessor(store, &recordingReceiptSender{err: errors.New("resend unavailable")}, 5)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := processor.ProcessOne(context.Background())
	if err != nil || !worked || store.failedID != validReceiptEmail.EventID || store.publishedID != "" {
		t.Fatalf("worked=%v err=%v failed=%q published=%q", worked, err, store.failedID, store.publishedID)
	}
}
