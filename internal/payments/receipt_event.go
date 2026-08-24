package payments

import (
	"errors"
	"strings"
	"time"
)

// ReceiptEvent contains only frozen, server-verified payment facts. Recipient
// e-mail is deliberately resolved by the delivery worker and is never queued.
type ReceiptEvent struct {
	EventID     string
	PaymentID   string
	CustomerID  string
	PlanName    string
	Reference   string
	AmountMinor int64
	Currency    string
	StartsAt    time.Time
	ExpiresAt   time.Time
}

func NewReceiptEvent(paymentID, customerID, planName, reference string, amountMinor int64, currency string, startsAt, expiresAt time.Time) (ReceiptEvent, error) {
	planName = strings.TrimSpace(planName)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if !validUUID(paymentID) || !validUUID(customerID) || planName == "" || !validReference(reference) || amountMinor <= 0 || !sameCurrency(currency, currency) || startsAt.IsZero() || expiresAt.IsZero() || !expiresAt.After(startsAt) {
		return ReceiptEvent{}, errors.New("payments: invalid receipt event")
	}
	return ReceiptEvent{
		EventID:     deterministicEventID("payment.receipt.requested", paymentID),
		PaymentID:   paymentID,
		CustomerID:  customerID,
		PlanName:    planName,
		Reference:   reference,
		AmountMinor: amountMinor,
		Currency:    currency,
		StartsAt:    startsAt.UTC(),
		ExpiresAt:   expiresAt.UTC(),
	}, nil
}
