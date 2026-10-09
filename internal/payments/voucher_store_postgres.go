package payments

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/netcore-isp/netcore/internal/billing"
)

const voucherOrderEndpoint = "POST /api/v1/voucher-orders"

func calculateVoucherQuote(planID, currency string, base, charge int64, bps, quantity int, reseller bool) (VoucherQuote, error) {
	if base <= 0 || charge < 0 || quantity < 1 || quantity > 100 {
		return VoucherQuote{}, ErrInvalidRequest
	}
	if !reseller {
		bps = 0
	}
	discount, net, err := billing.CalculateResellerPrice(base, bps)
	if err != nil || net > (math.MaxInt64-charge)/int64(quantity) {
		return VoucherQuote{}, ErrInvalidRequest
	}
	return VoucherQuote{PlanID: planID, Quantity: quantity, UnitBaseMinor: base, DiscountBasisPoints: bps, UnitDiscountMinor: discount, UnitNetMinor: net, BankChargeMinor: charge, TotalMinor: net*int64(quantity) + charge, Currency: strings.ToUpper(currency), Reseller: reseller}, nil
}

func (s *PostgresStore) QuoteVoucherOrder(ctx context.Context, tenantID, userID, planID string, quantity int) (VoucherQuote, error) {
	if !validUUID(tenantID) || !validUUID(userID) || !validUUID(planID) || quantity < 1 || quantity > 100 {
		return VoucherQuote{}, ErrInvalidRequest
	}
	var base, charge int64
	var bps int
	var currency string
	var reseller bool
	err := s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT p.price_minor,p.currency,bs.fixed_bank_charge_minor,bs.reseller_discount_basis_points,EXISTS(SELECT 1 FROM reseller_designations rd WHERE rd.tenant_id=u.tenant_id AND rd.user_id=u.id) FROM customers c JOIN users u ON u.id=c.user_id AND u.tenant_id=c.tenant_id JOIN plans p ON p.tenant_id=c.tenant_id AND p.id=$3::uuid AND p.status='ACTIVE' JOIN tenant_billing_settings bs ON bs.tenant_id=c.tenant_id WHERE c.tenant_id=$1::uuid AND c.user_id=$2::uuid AND c.status='ACTIVE' AND u.status='ACTIVE' AND u.email_verified_at IS NOT NULL AND p.price_minor>0`, tenantID, userID, planID).Scan(&base, &currency, &charge, &bps, &reseller)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return VoucherQuote{}, ErrPaymentNotFound
	}
	if err != nil {
		return VoucherQuote{}, err
	}
	return calculateVoucherQuote(planID, currency, base, charge, bps, quantity, reseller)
}

func (s *PostgresStore) PrepareVoucherOrder(ctx context.Context, in VoucherInitiation) (PendingVoucherOrder, *VoucherCheckout, error) {
	if !validUUID(in.TenantID) || !validUUID(in.UserID) || !validUUID(in.PlanID) || in.Quantity < 1 || in.Quantity > 100 || !validReference(in.Reference) || !validIdempotencyKey(in.IdempotencyKey) || len(in.RequestHash) != 32 {
		return PendingVoucherOrder{}, nil, ErrInvalidRequest
	}
	var pending PendingVoucherOrder
	var replay *VoucherCheckout
	err := s.db.InTenantTx(ctx, in.TenantID, func(tx pgx.Tx) error {
		var storedUser pgtype.Text
		var storedHash []byte
		var storedStatus pgtype.Int4
		var storedBody string
		lookup := tx.QueryRow(ctx, `SELECT user_id::text,request_hash,response_status,COALESCE(response_body::text,'{}') FROM idempotency_keys WHERE tenant_id=$1::uuid AND endpoint=$2 AND key=$3 FOR UPDATE`, in.TenantID, voucherOrderEndpoint, in.IdempotencyKey).Scan(&storedUser, &storedHash, &storedStatus, &storedBody)
		if lookup == nil {
			if !storedUser.Valid || storedUser.String != in.UserID || len(storedHash) != len(in.RequestHash) || subtle.ConstantTimeCompare(storedHash, in.RequestHash) != 1 {
				return ErrIdempotencyConflict
			}
			if storedStatus.Valid {
				var checkout VoucherCheckout
				if err := json.Unmarshal([]byte(storedBody), &checkout); err != nil || !validUUID(checkout.OrderID) || !validReference(checkout.Reference) || !validCheckoutURL(checkout.AuthorizationURL) {
					return ErrIdempotencyConflict
				}
				replay = &checkout
				return nil
			}
			var staging struct {
				OrderID string `json:"order_id"`
			}
			if err := json.Unmarshal([]byte(storedBody), &staging); err != nil || !validUUID(staging.OrderID) {
				return ErrIdempotencyConflict
			}
			return tx.QueryRow(ctx, `SELECT o.id::text,p.provider_reference,p.amount_minor,p.currency,COALESCE(c.email::text,u.email::text) FROM voucher_orders o JOIN payments p ON p.id=o.payment_id AND p.tenant_id=o.tenant_id JOIN customers c ON c.id=p.customer_id AND c.tenant_id=p.tenant_id JOIN users u ON u.id=c.user_id AND u.tenant_id=c.tenant_id WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid AND o.buyer_user_id=$3::uuid AND p.status='PENDING'`, in.TenantID, staging.OrderID, in.UserID).Scan(&pending.OrderID, &pending.Reference, &pending.AmountMinor, &pending.Currency, &pending.CustomerEmail)
		}
		if !errors.Is(lookup, pgx.ErrNoRows) {
			return lookup
		}
		var customerID, email, currency string
		var base, charge int64
		var bps int
		var reseller bool
		err := tx.QueryRow(ctx, `SELECT c.id::text,COALESCE(NULLIF(c.email::text,''),u.email::text,''),p.price_minor,p.currency,bs.fixed_bank_charge_minor,bs.reseller_discount_basis_points,EXISTS(SELECT 1 FROM reseller_designations rd WHERE rd.tenant_id=u.tenant_id AND rd.user_id=u.id) FROM customers c JOIN users u ON u.id=c.user_id AND u.tenant_id=c.tenant_id JOIN plans p ON p.tenant_id=c.tenant_id AND p.id=$3::uuid AND p.status='ACTIVE' JOIN tenant_billing_settings bs ON bs.tenant_id=c.tenant_id WHERE c.tenant_id=$1::uuid AND c.user_id=$2::uuid AND c.status='ACTIVE' AND u.status='ACTIVE' AND u.email_verified_at IS NOT NULL AND p.price_minor>0 FOR UPDATE OF u,p,bs`, in.TenantID, in.UserID, in.PlanID).Scan(&customerID, &email, &base, &currency, &charge, &bps, &reseller)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPaymentNotFound
		}
		if err != nil {
			return err
		}
		if email == "" {
			return ErrInvalidRequest
		}
		quote, err := calculateVoucherQuote(in.PlanID, currency, base, charge, bps, in.Quantity, reseller)
		if err != nil {
			return err
		}
		if quote.UnitBaseMinor > math.MaxInt64/int64(in.Quantity) || quote.UnitDiscountMinor > math.MaxInt64/int64(in.Quantity) {
			return ErrInvalidRequest
		}
		var paymentID string
		err = tx.QueryRow(ctx, `INSERT INTO payments(tenant_id,customer_id,subscription_id,gateway,provider_reference,amount_minor,plan_amount_minor,bank_charge_minor,currency,status,base_plan_amount_minor,reseller_discount_basis_points,reseller_discount_minor,reseller_at_initiation) VALUES($1::uuid,$2::uuid,NULL,$3,$4,$5,$6,$7,$8,'PENDING',$9,$10,$11,$12) RETURNING id::text`, in.TenantID, customerID, in.Gateway, in.Reference, quote.TotalMinor, quote.UnitNetMinor*int64(in.Quantity), quote.BankChargeMinor, quote.Currency, quote.UnitBaseMinor*int64(in.Quantity), quote.DiscountBasisPoints, quote.UnitDiscountMinor*int64(in.Quantity), quote.Reseller).Scan(&paymentID)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `INSERT INTO voucher_orders(tenant_id,buyer_user_id,plan_id,quantity,unit_base_minor,discount_basis_points,unit_discount_minor,unit_net_minor,bank_charge_minor,amount_minor,currency,reseller_at_initiation,payment_id,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::uuid,'PENDING') RETURNING id::text`, in.TenantID, in.UserID, in.PlanID, in.Quantity, quote.UnitBaseMinor, quote.DiscountBasisPoints, quote.UnitDiscountMinor, quote.UnitNetMinor, quote.BankChargeMinor, quote.TotalMinor, quote.Currency, quote.Reseller, paymentID).Scan(&pending.OrderID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idempotency_keys(tenant_id,user_id,endpoint,key,request_hash,response_body,expires_at) VALUES($1::uuid,$2::uuid,$3,$4,$5,jsonb_build_object('order_id',$6::text),now()+interval '24 hours')`, in.TenantID, in.UserID, voucherOrderEndpoint, in.IdempotencyKey, in.RequestHash, pending.OrderID); err != nil {
			return err
		}
		pending.Reference, pending.AmountMinor, pending.Currency, pending.CustomerEmail = in.Reference, quote.TotalMinor, quote.Currency, email
		return nil
	})
	if isIdempotencyConflict(err) {
		return s.PrepareVoucherOrder(ctx, in)
	}
	return pending, replay, err
}

func (s *PostgresStore) SaveVoucherCheckout(ctx context.Context, tenantID, key string, checkout VoucherCheckout) error {
	if !validUUID(tenantID) || !validUUID(checkout.OrderID) || !validReference(checkout.Reference) || !validCheckoutURL(checkout.AuthorizationURL) {
		return ErrInvalidRequest
	}
	body, err := json.Marshal(checkout)
	if err != nil {
		return err
	}
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `UPDATE idempotency_keys SET response_status=201,response_body=$4::jsonb WHERE tenant_id=$1::uuid AND endpoint=$2 AND key=$3 AND response_status IS NULL AND response_body ->> 'order_id' = $5::text`, tenantID, voucherOrderEndpoint, key, string(body), checkout.OrderID)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return ErrIdempotencyConflict
		}
		return nil
	})
}

func (s *PostgresStore) LoadVoucherOrder(ctx context.Context, tenantID, userID, orderID string) (VoucherOrder, error) {
	var order VoucherOrder
	if !validUUID(tenantID) || !validUUID(userID) || !validUUID(orderID) {
		return order, ErrInvalidRequest
	}
	err := s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT o.id::text,o.payment_id::text,o.plan_id::text,o.quantity,o.status,o.amount_minor,o.currency,COALESCE(b.id::text,'') FROM voucher_orders o LEFT JOIN voucher_batches b ON b.order_id=o.id AND b.tenant_id=o.tenant_id WHERE o.tenant_id=$1::uuid AND o.buyer_user_id=$2::uuid AND o.id=$3::uuid`, tenantID, userID, orderID).Scan(&order.ID, &order.PaymentID, &order.PlanID, &order.Quantity, &order.Status, &order.AmountMinor, &order.Currency, &order.BatchID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return VoucherOrder{}, ErrPaymentNotFound
	}
	return order, err
}

func (s *PostgresStore) MarkVoucherPaid(ctx context.Context, in Activation) (VoucherOrder, error) {
	var order VoucherOrder
	if !validUUID(in.TenantID) || !validUUID(in.UserID) || !validReference(in.Reference) || in.AmountMinor <= 0 || in.VerifiedAt.IsZero() {
		return order, ErrInvalidRequest
	}
	err := s.db.InTenantTx(ctx, in.TenantID, func(tx pgx.Tx) error {
		var paymentStatus string
		var paymentAmount int64
		var currency string
		if err := tx.QueryRow(ctx, `SELECT o.id::text,o.payment_id::text,o.plan_id::text,o.quantity,o.status,o.amount_minor,o.currency,p.status,p.amount_minor,p.currency FROM voucher_orders o JOIN payments p ON p.id=o.payment_id AND p.tenant_id=o.tenant_id JOIN customers c ON c.id=p.customer_id AND c.tenant_id=p.tenant_id WHERE o.tenant_id=$1::uuid AND o.buyer_user_id=$2::uuid AND c.user_id=$2::uuid AND p.gateway=$3 AND p.provider_reference=$4 FOR UPDATE OF o,p`, in.TenantID, in.UserID, in.Gateway, in.Reference).Scan(&order.ID, &order.PaymentID, &order.PlanID, &order.Quantity, &order.Status, &order.AmountMinor, &order.Currency, &paymentStatus, &paymentAmount, &currency); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrPaymentNotFound
			}
			return err
		}
		if paymentAmount != in.AmountMinor || order.AmountMinor != in.AmountMinor || !sameCurrency(currency, in.Currency) || !sameCurrency(order.Currency, in.Currency) {
			return ErrVerificationMismatch
		}
		if paymentStatus == StatusSuccess && order.Status == "PAID" {
			return nil
		}
		if paymentStatus != StatusPending || order.Status != "PENDING" {
			return ErrPaymentNotPending
		}
		if _, err := tx.Exec(ctx, `UPDATE payments SET status='SUCCESS',verified_at=$2::timestamptz,updated_at=now() WHERE id=$1::uuid AND status='PENDING'`, order.PaymentID, in.VerifiedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE voucher_orders SET status='PAID',paid_at=$2::timestamptz WHERE id=$1::uuid AND status='PENDING'`, order.ID, in.VerifiedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(tenant_id,actor_type,action,resource_type,resource_id,metadata) VALUES($1::uuid,'GATEWAY','VOUCHER_ORDER_PAID','voucher_orders',$2::uuid,jsonb_build_object('payment_id',$3::text,'amount_minor',$4::bigint))`, in.TenantID, order.ID, order.PaymentID, in.AmountMinor); err != nil {
			return err
		}
		return nil
	})
	return order, err
}
