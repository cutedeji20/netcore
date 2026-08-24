package payments

import (
	"context"
	"errors"
	"testing"
	"time"
)

const (
	paymentTestTenant = "11111111-1111-4111-8111-111111111111"
	paymentTestUser   = "22222222-2222-4222-8222-222222222222"
	paymentTestPlan   = "33333333-3333-4333-8333-333333333333"
)

type memoryPaymentStore struct {
	pending       PendingPayment
	payment       Payment
	replay        *Checkout
	prepareInput  Initiation
	activation    Activation
	prepareCalls  int
	saveCalls     int
	activateCalls int
	err           error
}

func (s *memoryPaymentStore) PrepareInitiation(_ context.Context, input Initiation) (PendingPayment, *Checkout, error) {
	s.prepareCalls++
	s.prepareInput = input
	if s.err != nil {
		return PendingPayment{}, nil, s.err
	}
	if s.replay != nil {
		return PendingPayment{}, s.replay, nil
	}
	pending := s.pending
	pending.Reference = input.Reference
	return pending, nil, nil
}
func (s *memoryPaymentStore) SaveCheckout(_ context.Context, _, _ string, _ Checkout) error {
	s.saveCalls++
	return s.err
}
func (s *memoryPaymentStore) PaymentForVerification(context.Context, string, string, string) (Payment, error) {
	return s.payment, s.err
}
func (s *memoryPaymentStore) ActivateVerified(_ context.Context, activation Activation) (ActivationResult, error) {
	s.activateCalls++
	s.activation = activation
	if s.err != nil {
		return ActivationResult{}, s.err
	}
	return ActivationResult{PaymentID: s.payment.ID, SubscriptionID: s.payment.SubscriptionID, Status: StatusSuccess}, nil
}

type memoryGateway struct {
	available      bool
	checkout       GatewayCheckout
	verify         GatewayVerification
	err            error
	initCalls      int
	verifyCalls    int
	initialization GatewayInitialization
}

func (g *memoryGateway) Name() string    { return "paystack" }
func (g *memoryGateway) Available() bool { return g.available }
func (g *memoryGateway) Initialize(_ context.Context, in GatewayInitialization) (GatewayCheckout, error) {
	g.initCalls++
	g.initialization = in
	return g.checkout, g.err
}
func (g *memoryGateway) Verify(context.Context, string) (GatewayVerification, error) {
	g.verifyCalls++
	return g.verify, g.err
}

func newPaymentService(t *testing.T, store *memoryPaymentStore, gateway *memoryGateway) *Service {
	t.Helper()
	service, err := NewService(store, gateway)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestInitiateFreezesServerPaymentAndReturnsOnlyCheckout(t *testing.T) {
	store := &memoryPaymentStore{pending: PendingPayment{ID: "payment", SubscriptionID: "subscription", AmountMinor: 500000, Currency: "NGN", CustomerEmail: "a@example.test"}}
	gateway := &memoryGateway{available: true, checkout: GatewayCheckout{AuthorizationURL: "https://checkout.example.test/pay"}}
	service := newPaymentService(t, store, gateway)

	checkout, err := service.Initiate(context.Background(), paymentTestTenant, paymentTestUser, paymentTestPlan, "payment-retry-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	if !validReference(checkout.Reference) || checkout.AuthorizationURL != "https://checkout.example.test/pay" {
		t.Fatalf("checkout = %+v", checkout)
	}
	if store.prepareInput.PlanID != paymentTestPlan || len(store.prepareInput.RequestHash) != 32 || store.prepareInput.Gateway != "paystack" {
		t.Fatalf("initiation was not server-bound: %+v", store.prepareInput)
	}
	if gateway.initialization.AmountMinor != 500000 || gateway.initialization.Currency != "NGN" || gateway.initialization.CustomerEmail != "a@example.test" || gateway.initialization.Reference != checkout.Reference {
		t.Fatalf("gateway initialization = %+v", gateway.initialization)
	}
	if store.saveCalls != 1 {
		t.Fatalf("checkout was not persisted for idempotent replay: save calls=%d", store.saveCalls)
	}
}

func TestInitiatePassesConfiguredCallbackURLToGateway(t *testing.T) {
	store := &memoryPaymentStore{pending: PendingPayment{ID: "payment", SubscriptionID: "subscription", AmountMinor: 500000, Currency: "NGN", CustomerEmail: "a@example.test"}}
	gateway := &memoryGateway{available: true, checkout: GatewayCheckout{AuthorizationURL: "https://checkout.example.test/pay"}}
	service, err := NewService(store, gateway, "https://portal.example.test/portal.html")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Initiate(context.Background(), paymentTestTenant, paymentTestUser, paymentTestPlan, "payment-retry-key-0001"); err != nil {
		t.Fatal(err)
	}
	if gateway.initialization.CallbackURL != "https://portal.example.test/portal.html" {
		t.Fatalf("gateway callback URL = %q", gateway.initialization.CallbackURL)
	}
}

func TestInitiateFailsBeforeCreatingPaymentWhenGatewayDisabled(t *testing.T) {
	store := &memoryPaymentStore{}
	service := newPaymentService(t, store, &memoryGateway{})
	_, err := service.Initiate(context.Background(), paymentTestTenant, paymentTestUser, paymentTestPlan, "payment-retry-key-0001")
	if !errors.Is(err, ErrGatewayUnavailable) || store.prepareCalls != 0 {
		t.Fatalf("err=%v prepare_calls=%d", err, store.prepareCalls)
	}
}

func TestVerifyActivatesOnlyAfterGatewayConfirmsFrozenFacts(t *testing.T) {
	reference := "pay_12345678901234567890123456789012"
	store := &memoryPaymentStore{payment: Payment{ID: "payment", SubscriptionID: "subscription", Gateway: "paystack", Reference: reference, AmountMinor: 500000, Currency: "NGN", Status: StatusPending}}
	gateway := &memoryGateway{available: true, verify: GatewayVerification{Reference: reference, Status: StatusSuccess, AmountMinor: 500000, Currency: "NGN", VerifiedAt: time.Now().UTC()}}
	service := newPaymentService(t, store, gateway)

	result, err := service.Verify(context.Background(), paymentTestTenant, paymentTestUser, reference)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess || gateway.verifyCalls != 1 || store.activateCalls != 1 || store.activation.AmountMinor != 500000 || store.activation.VerifiedAt.IsZero() {
		t.Fatalf("result=%+v verify=%d activate=%d input=%+v", result, gateway.verifyCalls, store.activateCalls, store.activation)
	}
}

func TestVerifyRejectsAmountMismatchWithoutActivation(t *testing.T) {
	reference := "pay_12345678901234567890123456789012"
	store := &memoryPaymentStore{payment: Payment{Gateway: "paystack", Reference: reference, AmountMinor: 500000, Currency: "NGN", Status: StatusPending}}
	gateway := &memoryGateway{available: true, verify: GatewayVerification{Reference: reference, Status: StatusSuccess, AmountMinor: 1, Currency: "NGN", VerifiedAt: time.Now().UTC()}}
	service := newPaymentService(t, store, gateway)

	_, err := service.Verify(context.Background(), paymentTestTenant, paymentTestUser, reference)
	if !errors.Is(err, ErrVerificationMismatch) || store.activateCalls != 0 {
		t.Fatalf("err=%v activate_calls=%d", err, store.activateCalls)
	}
}

func TestSuccessfulPaymentIsNotReverifiedOnRetry(t *testing.T) {
	reference := "pay_12345678901234567890123456789012"
	store := &memoryPaymentStore{payment: Payment{ID: "payment", SubscriptionID: "subscription", Gateway: "paystack", Reference: reference, AmountMinor: 500000, Currency: "NGN", Status: StatusSuccess}}
	gateway := &memoryGateway{available: true}
	service := newPaymentService(t, store, gateway)

	_, err := service.Verify(context.Background(), paymentTestTenant, paymentTestUser, reference)
	if err != nil || gateway.verifyCalls != 0 || store.activateCalls != 1 {
		t.Fatalf("err=%v verify_calls=%d activate_calls=%d", err, gateway.verifyCalls, store.activateCalls)
	}
}

func TestDeterministicEventID(t *testing.T) {
	if deterministicEventID("payment.succeeded", "paystack", "ref") != deterministicEventID("payment.succeeded", "paystack", "ref") {
		t.Fatal("same payment event did not derive the same event id")
	}
	if deterministicEventID("payment.succeeded", "paystack", "ref") == deterministicEventID("payment.succeeded", "paystack", "other") {
		t.Fatal("different payment events derived the same event id")
	}
}
