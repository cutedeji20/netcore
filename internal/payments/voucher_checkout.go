package payments

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/netcore-isp/netcore/internal/vouchers"
)

type VoucherQuote struct {
	PlanID              string `json:"plan_id"`
	Quantity            int    `json:"quantity"`
	UnitBaseMinor       int64  `json:"unit_base_minor"`
	DiscountBasisPoints int    `json:"discount_basis_points"`
	UnitDiscountMinor   int64  `json:"unit_discount_minor"`
	UnitNetMinor        int64  `json:"unit_net_minor"`
	BankChargeMinor     int64  `json:"bank_charge_minor"`
	TotalMinor          int64  `json:"total_minor"`
	Currency            string `json:"currency"`
	Reseller            bool   `json:"reseller"`
}

type VoucherOrder struct {
	ID          string `json:"id"`
	PaymentID   string `json:"-"`
	PlanID      string `json:"plan_id"`
	Quantity    int    `json:"quantity"`
	Status      string `json:"status"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	BatchID     string `json:"batch_id,omitempty"`
}

type VoucherCheckout struct {
	OrderID          string `json:"order_id"`
	Reference        string `json:"reference"`
	AuthorizationURL string `json:"authorization_url"`
}

type VoucherInitiation struct {
	TenantID, UserID, PlanID, Gateway, Reference, IdempotencyKey string
	Quantity                                                     int
	RequestHash                                                  []byte
}
type PendingVoucherOrder struct {
	OrderID, Reference, CustomerEmail, Currency string
	AmountMinor                                 int64
}

type VoucherOrderStore interface {
	QuoteVoucherOrder(context.Context, string, string, string, int) (VoucherQuote, error)
	PrepareVoucherOrder(context.Context, VoucherInitiation) (PendingVoucherOrder, *VoucherCheckout, error)
	SaveVoucherCheckout(context.Context, string, string, VoucherCheckout) error
	LoadVoucherOrder(context.Context, string, string, string) (VoucherOrder, error)
	MarkVoucherPaid(context.Context, Activation) (VoucherOrder, error)
}

type VoucherIssuer interface {
	IssueBatch(context.Context, string, vouchers.IssueRequest) (vouchers.Batch, error)
}

func (s *Service) ConfigureVoucherOrders(store VoucherOrderStore, issuer VoucherIssuer) error {
	if s == nil || store == nil || issuer == nil {
		return ErrInvalidRequest
	}
	s.voucherOrders, s.voucherIssuer = store, issuer
	return nil
}

func (s *Service) QuoteVoucherOrder(ctx context.Context, tenantID, userID, planID string, quantity int) (VoucherQuote, error) {
	if s == nil || s.voucherOrders == nil || !validUUID(tenantID) || !validUUID(userID) || !validUUID(planID) || quantity < 1 || quantity > 100 {
		return VoucherQuote{}, ErrInvalidRequest
	}
	return s.voucherOrders.QuoteVoucherOrder(ctx, tenantID, userID, planID, quantity)
}

func (s *Service) InitiateVoucherOrder(ctx context.Context, tenantID, userID, planID string, quantity int, key string) (VoucherCheckout, error) {
	if s == nil || s.voucherOrders == nil || s.gateway == nil || !validUUID(tenantID) || !validUUID(userID) || !validUUID(planID) || quantity < 1 || quantity > 100 || !validIdempotencyKey(key) {
		return VoucherCheckout{}, ErrInvalidRequest
	}
	if !s.gateway.Available() {
		return VoucherCheckout{}, ErrGatewayUnavailable
	}
	if probe, ok := s.gateway.(GatewayProbe); ok {
		if err := probe.Check(ctx); err != nil {
			return VoucherCheckout{}, ErrGatewayUnavailable
		}
	}
	reference, err := newReference()
	if err != nil {
		return VoucherCheckout{}, ErrGatewayUnavailable
	}
	hash := voucherInitiationHash(planID, quantity)
	pending, replay, err := s.voucherOrders.PrepareVoucherOrder(ctx, VoucherInitiation{TenantID: tenantID, UserID: userID, PlanID: planID, Quantity: quantity, Gateway: s.gateway.Name(), Reference: reference, IdempotencyKey: key, RequestHash: hash[:]})
	if err != nil {
		return VoucherCheckout{}, err
	}
	if replay != nil {
		return *replay, nil
	}
	gatewayCheckout, err := s.gateway.Initialize(ctx, GatewayInitialization{Reference: pending.Reference, AmountMinor: pending.AmountMinor, Currency: pending.Currency, CustomerEmail: pending.CustomerEmail, CallbackURL: s.callbackURL})
	if err != nil {
		return VoucherCheckout{}, fmt.Errorf("%w: %v", ErrGatewayUnavailable, err)
	}
	checkout := VoucherCheckout{OrderID: pending.OrderID, Reference: pending.Reference, AuthorizationURL: strings.TrimSpace(gatewayCheckout.AuthorizationURL)}
	if !validCheckoutURL(checkout.AuthorizationURL) {
		return VoucherCheckout{}, ErrGatewayUnavailable
	}
	if err := s.voucherOrders.SaveVoucherCheckout(ctx, tenantID, key, checkout); err != nil {
		return VoucherCheckout{}, err
	}
	return checkout, nil
}

func (s *Service) VoucherOrder(ctx context.Context, tenantID, userID, orderID string) (VoucherOrder, error) {
	if s == nil || s.voucherOrders == nil || !validUUID(tenantID) || !validUUID(userID) || !validUUID(orderID) {
		return VoucherOrder{}, ErrInvalidRequest
	}
	return s.voucherOrders.LoadVoucherOrder(ctx, tenantID, userID, orderID)
}

func (s *Service) finishVoucherVerification(ctx context.Context, activation Activation) (ActivationResult, error) {
	order, err := s.voucherOrders.MarkVoucherPaid(ctx, activation)
	if err != nil {
		return ActivationResult{}, err
	}
	batch, err := s.voucherIssuer.IssueBatch(ctx, activation.TenantID, vouchers.IssueRequest{PlanID: order.PlanID, Quantity: order.Quantity, OrderID: order.ID, BuyerUserID: activation.UserID})
	if err != nil {
		return ActivationResult{}, err
	}
	_ = batch
	return ActivationResult{PaymentID: order.PaymentID, VoucherOrderID: order.ID, Status: StatusSuccess}, nil
}

func voucherInitiationHash(planID string, quantity int) [32]byte {
	return sha256.Sum256([]byte(fmt.Sprintf("%s:%d", planID, quantity)))
}
