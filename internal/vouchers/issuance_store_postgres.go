package vouchers

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/netcore-isp/netcore/pkg/crypto/envelope"
)

func (s *PostgresStore) SaveBatch(ctx context.Context, tenantID string, request IssueRequest, batchID string, codes []sealedCode) (Batch, error) {
	if s == nil || tenantID == "" || len(codes) != request.Quantity {
		return Batch{}, ErrIssueInvalid
	}
	var batch Batch
	err := s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if request.OrderID != "" {
			var existingID string
			err := tx.QueryRow(ctx, `SELECT id::text FROM voucher_batches WHERE tenant_id=$1::uuid AND order_id=$2::uuid`, tenantID, request.OrderID).Scan(&existingID)
			if err == nil {
				return loadIssuedBatch(ctx, tx, tenantID, existingID, &batch)
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			var valid bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM voucher_orders o JOIN payments p ON p.id=o.payment_id AND p.tenant_id=o.tenant_id WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid AND o.buyer_user_id=$3::uuid AND o.plan_id=$4::uuid AND o.quantity=$5 AND o.status='PAID' AND p.status='SUCCESS' AND p.amount_minor=o.amount_minor)`, tenantID, request.OrderID, request.BuyerUserID, request.PlanID, request.Quantity).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return ErrIssueInvalid
			}
		} else {
			var administrator bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id AND r.tenant_id=u.tenant_id WHERE u.tenant_id=$1::uuid AND u.id=$2::uuid AND u.status='ACTIVE' AND r.name='Administrator')`, tenantID, request.IssuerUserID).Scan(&administrator); err != nil {
				return err
			}
			if !administrator {
				return ErrIssueInvalid
			}
		}
		var planName string
		if err := tx.QueryRow(ctx, `SELECT name FROM plans WHERE tenant_id=$1::uuid AND id=$2::uuid AND ($3::boolean OR status='ACTIVE') FOR UPDATE`, tenantID, request.PlanID, request.OrderID != "").Scan(&planName); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrIssueInvalid
			}
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO voucher_batches(id,tenant_id,plan_id,order_id,buyer_user_id,issuer_user_id,quantity,reason,expires_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7,$8,$9)`, batchID, tenantID, request.PlanID, nullableVoucherUUID(request.OrderID), nullableVoucherUUID(request.BuyerUserID), nullableVoucherUUID(request.IssuerUserID), request.Quantity, nullableVoucherText(request.Reason), request.ExpiresAt); err != nil {
			return err
		}
		for _, code := range codes {
			var orderPosition any
			if request.OrderID != "" {
				orderPosition = code.position
			}
			if _, err := tx.Exec(ctx, `INSERT INTO vouchers(id,tenant_id,code_hash,code_prefix,plan_id,batch_id,status,expires_at,order_id,order_position,code_ciphertext,code_nonce,code_wrapped_dek,code_kek_key_id) VALUES($1::uuid,$2::uuid,$3,$4,$5::uuid,$6::uuid,'UNUSED',$7,$8::uuid,$9,$10,$11,$12,$13)`, code.id, tenantID, code.hash[:], normalizeCode(code.code)[:4], request.PlanID, batchID, request.ExpiresAt, nullableVoucherUUID(request.OrderID), orderPosition, code.record.Ciphertext, code.record.Nonce, code.record.WrappedDEK, code.record.KEKKeyID); err != nil {
				return err
			}
		}
		actorID := request.IssuerUserID
		actorType := "STAFF"
		if request.OrderID != "" {
			actorID = request.BuyerUserID
			actorType = "SYSTEM"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(tenant_id,actor_type,actor_id,action,resource_type,resource_id,metadata) VALUES($1::uuid,$2,$3::uuid,'VOUCHER_BATCH_ISSUED','voucher_batches',$4::uuid,jsonb_build_object('quantity',$5::integer,'plan_id',$6::text,'order_id',$7::text))`, tenantID, actorType, actorID, batchID, request.Quantity, request.PlanID, request.OrderID); err != nil {
			return err
		}
		batch = Batch{ID: batchID, PlanName: planName, Issued: int64(request.Quantity), Available: int64(request.Quantity), Status: "ACTIVE", ExpiresAt: request.ExpiresAt, CreatedAt: time.Now().UTC()}
		return nil
	})
	if request.OrderID != "" && err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT id::text FROM voucher_batches WHERE tenant_id=$1::uuid AND order_id=$2::uuid`, tenantID, request.OrderID).Scan(&batch.ID)
			})
			if err == nil {
				err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error { return loadIssuedBatch(ctx, tx, tenantID, batch.ID, &batch) })
			}
		}
	}
	return batch, err
}

func loadIssuedBatch(ctx context.Context, tx pgx.Tx, tenantID, batchID string, batch *Batch) error {
	err := tx.QueryRow(ctx, `SELECT b.id::text,p.name,b.quantity::bigint,count(v.id) FILTER (WHERE v.status='REDEEMED')::bigint,count(v.id) FILTER (WHERE v.status='UNUSED')::bigint,b.expires_at,b.created_at FROM voucher_batches b JOIN plans p ON p.id=b.plan_id AND p.tenant_id=b.tenant_id LEFT JOIN vouchers v ON v.tenant_id=b.tenant_id AND v.batch_id=b.id WHERE b.tenant_id=$1::uuid AND b.id=$2::uuid GROUP BY b.id,p.name`, tenantID, batchID).Scan(&batch.ID, &batch.PlanName, &batch.Issued, &batch.Redeemed, &batch.Available, &batch.ExpiresAt, &batch.CreatedAt)
	if err != nil {
		return err
	}
	batch.Status = "ACTIVE"
	if batch.Available == 0 {
		batch.Status = "COMPLETED"
	}
	if batch.ExpiresAt != nil && batch.ExpiresAt.Before(time.Now()) {
		batch.Status = "EXPIRED"
	}
	return nil
}

func (s *PostgresStore) RevealBatch(ctx context.Context, wrapper envelope.KeyWrapper, tenantID, actorUserID, batchID string) ([]IssuedCode, error) {
	var codes []IssuedCode
	err := s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var authorized bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM voucher_batches b JOIN users u ON u.id=$3::uuid AND u.tenant_id=b.tenant_id AND u.status='ACTIVE' WHERE b.tenant_id=$1::uuid AND b.id=$2::uuid AND (b.buyer_user_id=u.id OR b.issuer_user_id=u.id))`, tenantID, batchID, actorUserID).Scan(&authorized); err != nil {
			return err
		}
		if !authorized {
			return ErrCodeUnavailable
		}
		rows, err := tx.Query(ctx, `SELECT id::text,code_ciphertext,code_nonce,code_wrapped_dek,code_kek_key_id FROM vouchers WHERE tenant_id=$1::uuid AND batch_id=$2::uuid ORDER BY order_position NULLS LAST,id`, tenantID, batchID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var record CodeEnvelope
			if err := rows.Scan(&id, &record.Ciphertext, &record.Nonce, &record.WrappedDEK, &record.KEKKeyID); err != nil {
				return err
			}
			code, err := OpenCode(ctx, wrapper, tenantID, id, record)
			if err != nil {
				return ErrCodeUnavailable
			}
			codes = append(codes, IssuedCode{VoucherID: id, Code: code})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		_, err = tx.Exec(ctx, `INSERT INTO audit_logs(tenant_id,actor_type,actor_id,action,resource_type,resource_id,metadata) VALUES($1::uuid,'USER',$2::uuid,'VOUCHER_BATCH_REVEALED','voucher_batches',$3::uuid,jsonb_build_object('quantity',$4::integer))`, tenantID, actorUserID, batchID, len(codes))
		return err
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

func nullableVoucherUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nullableVoucherText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
