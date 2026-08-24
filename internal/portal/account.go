package portal

import (
	"context"
	"errors"
	"time"
)

var (
	ErrCustomerAccountUnavailable = errors.New("portal: customer account unavailable")
	ErrCustomerAccountNotFound    = errors.New("portal: customer account not found")
)

// CustomerSubscription is the customer-safe subscription summary. Internal
// identifiers, enforcement data, and usage records remain server-side.
type CustomerSubscription struct {
	PlanName      string
	Status        string
	PaymentStatus string
	StartsAt      *time.Time
	ExpiresAt     *time.Time
}

// CustomerPayment is a customer-safe payment history entry. It intentionally
// omits gateway payloads and credentials.
type CustomerPayment struct {
	Reference   string
	AmountMinor int64
	Currency    string
	Status      string
	CreatedAt   time.Time
}

type CustomerAccount struct {
	Subscriptions []CustomerSubscription
	Payments      []CustomerPayment
}

// AccountStore must scope all reads to the authenticated principal. The
// browser never supplies either identity value.
type AccountStore interface {
	CustomerAccount(ctx context.Context, tenantID, userID string) (CustomerAccount, bool, error)
}

type AccountService struct{ store AccountStore }

func NewAccountService(store AccountStore) (*AccountService, error) {
	if store == nil {
		return nil, errors.New("portal: customer account store is required")
	}
	return &AccountService{store: store}, nil
}

func (s *AccountService) Account(ctx context.Context, tenantID, userID string) (CustomerAccount, error) {
	if s == nil || s.store == nil || !validUUID(tenantID) || !validUUID(userID) {
		return CustomerAccount{}, ErrCustomerAccountUnavailable
	}
	account, found, err := s.store.CustomerAccount(ctx, tenantID, userID)
	if err != nil {
		return CustomerAccount{}, ErrCustomerAccountUnavailable
	}
	if !found {
		return CustomerAccount{}, ErrCustomerAccountNotFound
	}
	return account, nil
}
