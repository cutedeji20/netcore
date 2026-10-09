package vouchers

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/netcore-isp/netcore/pkg/crypto/envelope"
)

var ErrIssueInvalid = errors.New("vouchers: issuance invalid")

type IssueRequest struct {
	PlanID       string
	Quantity     int
	OrderID      string
	BuyerUserID  string
	IssuerUserID string
	Reason       string
	ExpiresAt    *time.Time
}

type IssuedCode struct {
	VoucherID string `json:"voucher_id"`
	Code      string `json:"code"`
}

type sealedCode struct {
	id       string
	code     string
	hash     [32]byte
	record   CodeEnvelope
	position int
}

type IssuanceStore interface {
	SaveBatch(context.Context, string, IssueRequest, string, []sealedCode) (Batch, error)
	RevealBatch(context.Context, envelope.KeyWrapper, string, string, string) ([]IssuedCode, error)
}

type Issuer struct {
	store   IssuanceStore
	wrapper envelope.KeyWrapper
	keyID   string
}

func NewIssuer(store IssuanceStore, wrapper envelope.KeyWrapper, keyID string) (*Issuer, error) {
	if store == nil || wrapper == nil || keyID == "" {
		return nil, ErrCodeUnavailable
	}
	return &Issuer{store: store, wrapper: wrapper, keyID: keyID}, nil
}

func (s *Issuer) IssueBatch(ctx context.Context, tenantID string, request IssueRequest) (Batch, error) {
	if s == nil || tenantID == "" || request.PlanID == "" || request.Quantity < 1 || request.Quantity > 100 {
		return Batch{}, ErrIssueInvalid
	}
	if request.OrderID == "" && (request.IssuerUserID == "" || request.Reason == "") {
		return Batch{}, ErrIssueInvalid
	}
	if request.OrderID != "" && request.BuyerUserID == "" {
		return Batch{}, ErrIssueInvalid
	}
	batchID := uuid.NewString()
	codes := make([]sealedCode, 0, request.Quantity)
	for position := 1; position <= request.Quantity; position++ {
		id := uuid.NewString()
		code, err := NewCode()
		if err != nil {
			return Batch{}, ErrCodeUnavailable
		}
		record, err := SealCode(ctx, s.wrapper, tenantID, id, code)
		if err != nil || record.KEKKeyID != s.keyID {
			return Batch{}, ErrCodeUnavailable
		}
		codes = append(codes, sealedCode{id: id, code: code, hash: HashCode(code), record: record, position: position})
	}
	return s.store.SaveBatch(ctx, tenantID, request, batchID, codes)
}

func (s *Issuer) RevealBatch(ctx context.Context, tenantID, actorUserID, batchID string) ([]IssuedCode, error) {
	if s == nil || tenantID == "" || actorUserID == "" || batchID == "" {
		return nil, ErrCodeUnavailable
	}
	return s.store.RevealBatch(ctx, s.wrapper, tenantID, actorUserID, batchID)
}
