// Package payments owns customer payment initiation, independent gateway
// verification, and the activation boundary.  A browser can ask to begin or
// check a payment; it can never submit an amount or mark access as paid.
package payments

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var (
	ErrInvalidRequest       = errors.New("payments: invalid request")
	ErrGatewayUnavailable   = errors.New("payments: gateway unavailable")
	ErrGatewayRejected      = errors.New("payments: gateway rejected payment")
	ErrPaymentNotFound      = errors.New("payments: payment not found")
	ErrPaymentNotPending    = errors.New("payments: payment is not pending")
	ErrIdempotencyConflict  = errors.New("payments: idempotency key reused with a different request")
	ErrVerificationMismatch = errors.New("payments: verification does not match pending payment")
	ErrWebhookInvalid       = errors.New("payments: invalid webhook")
	ErrWebhookReplay        = errors.New("payments: webhook replay payload mismatch")
)

const (
	StatusPending = "PENDING"
	StatusSuccess = "SUCCESS"
)

// Gateway only accepts a server-generated reference and server-frozen money
// values. Implementations must make both calls over TLS and set bounded
// timeouts; neither method is ever called inside a database transaction.
type Gateway interface {
	Name() string
	Available() bool
	Initialize(context.Context, GatewayInitialization) (GatewayCheckout, error)
	Verify(context.Context, string) (GatewayVerification, error)
}

type GatewayInitialization struct {
	Reference     string
	AmountMinor   int64
	Currency      string
	CustomerEmail string
}

type GatewayCheckout struct {
	AuthorizationURL string
}

type GatewayVerification struct {
	Reference   string
	Status      string
	AmountMinor int64
	Currency    string
	VerifiedAt  time.Time
}

// Store is deliberately narrow: it owns all tenant SQL while Service owns
// validation and makes the gateway call outside the database transaction.
type Store interface {
	PrepareInitiation(context.Context, Initiation) (PendingPayment, *Checkout, error)
	SaveCheckout(context.Context, string, string, Checkout) error
	PaymentForVerification(context.Context, string, string, string) (Payment, error)
	ActivateVerified(context.Context, Activation) (ActivationResult, error)
}

type Initiation struct {
	TenantID       string
	UserID         string
	PlanID         string
	Gateway        string
	Reference      string
	IdempotencyKey string
	RequestHash    []byte
}

type PendingPayment struct {
	ID             string
	SubscriptionID string
	Reference      string
	AmountMinor    int64
	Currency       string
	CustomerEmail  string
}

// Checkout is safe to return to a browser. It contains no gateway secret,
// account record, MAC address, or customer information.
type Checkout struct {
	Reference        string `json:"reference"`
	AuthorizationURL string `json:"authorization_url"`
}

type Payment struct {
	ID             string
	SubscriptionID string
	Gateway        string
	Reference      string
	AmountMinor    int64
	Currency       string
	Status         string
}

type Activation struct {
	TenantID    string
	UserID      string
	Gateway     string
	Reference   string
	AmountMinor int64
	Currency    string
	VerifiedAt  time.Time
}

type ActivationResult struct {
	PaymentID        string
	SubscriptionID   string
	Status           string
	StartsAt         time.Time
	ExpiresAt        time.Time
	AlreadyActivated bool
}

// Service deliberately does not trust any browser success redirect.  The
// only path to ActivateVerified follows Gateway.Verify.
type Service struct {
	store   Store
	gateway Gateway
}

func NewService(store Store, gateway Gateway) (*Service, error) {
	if store == nil || gateway == nil {
		return nil, errors.New("payments: store and gateway are required")
	}
	if strings.TrimSpace(gateway.Name()) == "" {
		return nil, errors.New("payments: gateway name is required")
	}
	return &Service{store: store, gateway: gateway}, nil
}

// Initiate freezes the plan price in a PENDING payment, then begins checkout.
// The key binds a customer retry to exactly one frozen payment.
func (s *Service) Initiate(ctx context.Context, tenantID, userID, planID, idempotencyKey string) (Checkout, error) {
	if s == nil || s.store == nil || s.gateway == nil || !validUUID(tenantID) || !validUUID(userID) || !validUUID(planID) || !validIdempotencyKey(idempotencyKey) {
		return Checkout{}, ErrInvalidRequest
	}
	if !s.gateway.Available() {
		return Checkout{}, ErrGatewayUnavailable
	}
	reference, err := newReference()
	if err != nil {
		return Checkout{}, fmt.Errorf("%w: generate provider reference", ErrGatewayUnavailable)
	}
	requestHash := initiationHash(planID)
	pending, replay, err := s.store.PrepareInitiation(ctx, Initiation{
		TenantID: tenantID, UserID: userID, PlanID: planID, Gateway: s.gateway.Name(),
		Reference: reference, IdempotencyKey: idempotencyKey, RequestHash: requestHash,
	})
	if err != nil {
		return Checkout{}, err
	}
	if replay != nil {
		return *replay, nil
	}

	gatewayCheckout, err := s.gateway.Initialize(ctx, GatewayInitialization{
		Reference: pending.Reference, AmountMinor: pending.AmountMinor,
		Currency: pending.Currency, CustomerEmail: pending.CustomerEmail,
	})
	if err != nil {
		return Checkout{}, fmt.Errorf("%w: %v", ErrGatewayUnavailable, err)
	}
	checkout := Checkout{Reference: pending.Reference, AuthorizationURL: strings.TrimSpace(gatewayCheckout.AuthorizationURL)}
	if !validCheckoutURL(checkout.AuthorizationURL) {
		return Checkout{}, ErrGatewayUnavailable
	}
	if err := s.store.SaveCheckout(ctx, tenantID, idempotencyKey, checkout); err != nil {
		return Checkout{}, err
	}
	return checkout, nil
}

// Verify obtains facts from the gateway itself before it can activate access.
// This method is suitable for a webhook worker or a customer status poll; in
// both cases the browser's claim is not treated as proof of payment.
func (s *Service) Verify(ctx context.Context, tenantID, userID, reference string) (ActivationResult, error) {
	if s == nil || s.store == nil || s.gateway == nil || !validUUID(tenantID) || !validUUID(userID) || !validReference(reference) {
		return ActivationResult{}, ErrInvalidRequest
	}
	if !s.gateway.Available() {
		return ActivationResult{}, ErrGatewayUnavailable
	}
	payment, err := s.store.PaymentForVerification(ctx, tenantID, userID, reference)
	if err != nil || payment.Gateway != s.gateway.Name() {
		if err != nil {
			return ActivationResult{}, err
		}
		return ActivationResult{}, ErrPaymentNotFound
	}
	if payment.Status == StatusSuccess {
		// The store returns the dates only from the successful activation path.
		return s.store.ActivateVerified(ctx, Activation{TenantID: tenantID, UserID: userID, Gateway: payment.Gateway, Reference: payment.Reference, AmountMinor: payment.AmountMinor, Currency: payment.Currency, VerifiedAt: time.Now().UTC()})
	}
	if payment.Status != StatusPending {
		return ActivationResult{}, ErrPaymentNotPending
	}
	verification, err := s.gateway.Verify(ctx, payment.Reference)
	if err != nil {
		return ActivationResult{}, fmt.Errorf("%w: %v", ErrGatewayUnavailable, err)
	}
	if verification.Status != StatusSuccess {
		return ActivationResult{}, ErrGatewayRejected
	}
	if verification.Reference != payment.Reference || verification.AmountMinor != payment.AmountMinor || !sameCurrency(verification.Currency, payment.Currency) || verification.VerifiedAt.IsZero() {
		return ActivationResult{}, ErrVerificationMismatch
	}
	return s.store.ActivateVerified(ctx, Activation{
		TenantID: tenantID, UserID: userID, Gateway: payment.Gateway,
		Reference: payment.Reference, AmountMinor: verification.AmountMinor,
		Currency: payment.Currency, VerifiedAt: verification.VerifiedAt.UTC(),
	})
}

func validCheckoutURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

func sameCurrency(a, b string) bool {
	return strings.ToUpper(strings.TrimSpace(a)) == strings.ToUpper(strings.TrimSpace(b)) && len(strings.TrimSpace(a)) == 3
}

func initiationHash(planID string) []byte {
	// The request has one mutable business input.  Hashing this canonical value
	// lets the database distinguish a retry from a different purchase reused
	// under the same key.
	sum := sha256.Sum256([]byte("plan_id=" + planID))
	return sum[:]
}

func newReference() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	// Paystack permits alphanumerics plus '-', '.', and '=' in a reference.
	// A URL-safe base64 value would occasionally produce '_', and the old
	// `pay_` prefix was itself outside that documented alphabet. Hex gives a
	// stable 36-character provider-compatible reference without weakening its
	// cryptographic unpredictability.
	return "pay-" + hex.EncodeToString(value[:]), nil
}

func validIdempotencyKey(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 16 || len(value) > 200 {
		return false
	}
	for _, c := range value {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

func validReference(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 36 || (!strings.HasPrefix(value, "pay-") && !strings.HasPrefix(value, "pay_")) {
		return false
	}
	for _, char := range value[4:] {
		if !((char >= 'a' && char <= 'f') || (char >= '0' && char <= '9')) {
			return false
		}
	}
	return true
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if value[i] != '-' {
				return false
			}
			continue
		}
		if !((value[i] >= '0' && value[i] <= '9') || (value[i] >= 'a' && value[i] <= 'f') || (value[i] >= 'A' && value[i] <= 'F')) {
			return false
		}
	}
	return true
}
