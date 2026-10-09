package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/netcore-isp/netcore/internal/vouchers"
)

type voucherOrderStoreStub struct {
	pending      PendingVoucherOrder
	paid         VoucherOrder
	marks        int
	prepareCalls int
}

func (*voucherOrderStoreStub) QuoteVoucherOrder(context.Context, string, string, string, int) (VoucherQuote, error) {
	return VoucherQuote{}, nil
}
func (s *voucherOrderStoreStub) PrepareVoucherOrder(_ context.Context, in VoucherInitiation) (PendingVoucherOrder, *VoucherCheckout, error) {
	s.prepareCalls++
	p := s.pending
	p.Reference = in.Reference
	return p, nil, nil
}
func (*voucherOrderStoreStub) SaveVoucherCheckout(context.Context, string, string, VoucherCheckout) error {
	return nil
}
func (*voucherOrderStoreStub) LoadVoucherOrder(context.Context, string, string, string) (VoucherOrder, error) {
	return VoucherOrder{}, nil
}
func (s *voucherOrderStoreStub) MarkVoucherPaid(context.Context, Activation) (VoucherOrder, error) {
	s.marks++
	return s.paid, nil
}

type voucherIssuerStub struct {
	calls    int
	quantity int
}

func (s *voucherIssuerStub) IssueBatch(_ context.Context, _ string, request vouchers.IssueRequest) (vouchers.Batch, error) {
	s.calls++
	s.quantity = request.Quantity
	return vouchers.Batch{ID: "batch", Issued: int64(request.Quantity)}, nil
}

func TestVoucherOrderRejectsInvalidQuantityBeforeGateway(t *testing.T) {
	store := &voucherOrderStoreStub{}
	gateway := &memoryGateway{available: true}
	s := newPaymentService(t, &memoryPaymentStore{}, gateway)
	issuer := &voucherIssuerStub{}
	if err := s.ConfigureVoucherOrders(store, issuer); err != nil {
		t.Fatal(err)
	}
	for _, quantity := range []int{0, 101} {
		if _, err := s.InitiateVoucherOrder(context.Background(), paymentTestTenant, paymentTestUser, paymentTestPlan, quantity, "voucher-key-0001"); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("quantity=%d err=%v", quantity, err)
		}
	}
	if store.prepareCalls != 0 || gateway.initCalls != 0 {
		t.Fatal("invalid order touched gateway or store")
	}
}

func TestVoucherOrderGatewayGetsFrozenTotalWithoutBuyerSubscription(t *testing.T) {
	store := &voucherOrderStoreStub{pending: PendingVoucherOrder{OrderID: "55555555-5555-4555-8555-555555555555", AmountMinor: 9001, Currency: "NGN", CustomerEmail: "buyer@example.test"}}
	gateway := &memoryGateway{available: true, checkout: GatewayCheckout{AuthorizationURL: "https://checkout.example.test/pay"}}
	s := newPaymentService(t, &memoryPaymentStore{}, gateway)
	if err := s.ConfigureVoucherOrders(store, &voucherIssuerStub{}); err != nil {
		t.Fatal(err)
	}
	checkout, err := s.InitiateVoucherOrder(context.Background(), paymentTestTenant, paymentTestUser, paymentTestPlan, 3, "voucher-key-0001")
	if err != nil || checkout.OrderID != store.pending.OrderID || gateway.initialization.AmountMinor != 9001 {
		t.Fatalf("checkout=%+v gateway=%+v err=%v", checkout, gateway.initialization, err)
	}
}

func TestVoucherVerificationIssuesOnlyAfterGatewaySuccessAndRetriesIdempotently(t *testing.T) {
	reference := "pay_12345678901234567890123456789012"
	payment := Payment{ID: "66666666-6666-4666-8666-666666666666", VoucherOrderID: "55555555-5555-4555-8555-555555555555", Gateway: "paystack", Reference: reference, AmountMinor: 9001, Currency: "NGN", Status: StatusPending}
	baseStore := &memoryPaymentStore{payment: payment}
	orderStore := &voucherOrderStoreStub{paid: VoucherOrder{ID: payment.VoucherOrderID, PaymentID: payment.ID, PlanID: paymentTestPlan, Quantity: 3, Status: "PAID"}}
	issuer := &voucherIssuerStub{}
	gateway := &memoryGateway{available: true, verify: GatewayVerification{Reference: reference, Status: StatusSuccess, AmountMinor: 9001, Currency: "NGN", VerifiedAt: time.Now().UTC()}}
	s := newPaymentService(t, baseStore, gateway)
	if err := s.ConfigureVoucherOrders(orderStore, issuer); err != nil {
		t.Fatal(err)
	}
	result, err := s.Verify(context.Background(), paymentTestTenant, paymentTestUser, reference)
	if err != nil || result.VoucherOrderID != payment.VoucherOrderID || issuer.calls != 1 || issuer.quantity != 3 || baseStore.activateCalls != 0 {
		t.Fatalf("result=%+v err=%v issuer=%+v activate=%d", result, err, issuer, baseStore.activateCalls)
	}
	baseStore.payment.Status = StatusSuccess
	if _, err := s.Verify(context.Background(), paymentTestTenant, paymentTestUser, reference); err != nil || issuer.calls != 2 || gateway.verifyCalls != 1 {
		t.Fatalf("retry err=%v issuer=%d gateway=%d", err, issuer.calls, gateway.verifyCalls)
	}
}

func TestFailedVoucherPaymentIssuesNoCodes(t *testing.T) {
	reference := "pay_12345678901234567890123456789012"
	baseStore := &memoryPaymentStore{payment: Payment{VoucherOrderID: "55555555-5555-4555-8555-555555555555", Gateway: "paystack", Reference: reference, AmountMinor: 9001, Currency: "NGN", Status: StatusPending}}
	issuer := &voucherIssuerStub{}
	gateway := &memoryGateway{available: true, verify: GatewayVerification{Reference: reference, Status: "FAILED", AmountMinor: 9001, Currency: "NGN"}}
	s := newPaymentService(t, baseStore, gateway)
	if err := s.ConfigureVoucherOrders(&voucherOrderStoreStub{}, issuer); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(context.Background(), paymentTestTenant, paymentTestUser, reference); !errors.Is(err, ErrGatewayRejected) || issuer.calls != 0 {
		t.Fatalf("err=%v issued=%d", err, issuer.calls)
	}
}

func TestVoucherQuoteTotalsAndOverflow(t *testing.T) {
	quote, err := calculateVoucherQuote(paymentTestPlan, "NGN", 10001, 1500, 2500, 3, true)
	if err != nil || quote.UnitDiscountMinor != 2500 || quote.TotalMinor != 24003 {
		t.Fatalf("quote=%+v err=%v", quote, err)
	}
	if _, err := calculateVoucherQuote(paymentTestPlan, "NGN", 9223372036854775807, 1500, 0, 100, false); err == nil {
		t.Fatal("overflow accepted")
	}
}
