package portal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) Candidate(ctx context.Context, tenantID, userID, subscriptionID, targetMAC, nasAddress string) (candidate ReplacementCandidate, err error) {
	if !validUUID(tenantID) || !validUUID(userID) || !validUUID(subscriptionID) {
		return candidate, ErrReplacementNotEligible
	}
	candidate.SubscriptionID = subscriptionID
	candidate.TargetMAC = targetMAC
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
SELECT u.email::text,s.customer_id::text,s.device_id::text,n.id::text
 FROM subscriptions s
 JOIN customers c ON c.id=s.customer_id AND c.tenant_id=s.tenant_id AND c.status='ACTIVE' AND c.user_id=$2::uuid
 JOIN users u ON u.id=c.user_id AND u.tenant_id=c.tenant_id AND u.email_verified_at IS NOT NULL
 JOIN plans p ON p.id=s.plan_id AND p.tenant_id=s.tenant_id AND p.status='ACTIVE'
 JOIN nas n ON n.tenant_id=s.tenant_id AND n.hotspot_address=$5::inet AND n.status='ACTIVE'
 JOIN devices old ON old.id=s.device_id AND old.tenant_id=s.tenant_id AND old.customer_id=s.customer_id AND old.status='ACTIVE'
 WHERE s.tenant_id=$1::uuid AND s.id=$3::uuid AND s.status='ACTIVE' AND s.starts_at<=now() AND s.expires_at>now()
   AND old.normalized_mac<>$4
   AND NOT EXISTS(SELECT 1 FROM sessions ss WHERE ss.tenant_id=s.tenant_id AND ss.subscription_id=s.id AND ss.status<>'CLOSED')
   AND NOT subscription_has_radius_reservation(s.tenant_id,s.id)
   AND NOT EXISTS(SELECT 1 FROM device_replacements dr WHERE dr.tenant_id=s.tenant_id AND dr.subscription_id=s.id AND dr.status IN ('CHALLENGE','PENDING') AND dr.expires_at>now())
   AND (p.quota_bytes IS NULL OR EXISTS(SELECT 1 FROM usage_counters uc WHERE uc.tenant_id=s.tenant_id AND uc.subscription_id=s.id AND uc.period_start<=now() AND uc.period_end>now() AND uc.exhausted_at IS NULL AND uc.consumed_bytes<uc.quota_bytes))
   AND NOT EXISTS(SELECT 1 FROM devices other WHERE other.tenant_id=s.tenant_id AND other.normalized_mac=$4 AND other.status<>'REMOVED' AND other.customer_id<>s.customer_id)
 LIMIT 1`, tenantID, userID, subscriptionID, targetMAC, nasAddress).Scan(&candidate.Email, &candidate.CustomerID, &candidate.OldDeviceID, &candidate.NASID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ReplacementCandidate{}, ErrReplacementNotEligible
	}
	return candidate, err
}

func (s *PostgresStore) CreateChallenge(ctx context.Context, tenantID, userID string, c ReplacementCandidate, challengeID string, expiry time.Time) error {
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE device_replacements SET status='CANCELLED' WHERE tenant_id=$1::uuid AND subscription_id=$2::uuid AND status IN ('CHALLENGE','PENDING') AND expires_at<=now()`, tenantID, c.SubscriptionID); err != nil {
			return fmt.Errorf("expire device challenge: %w", err)
		}
		_, err := tx.Exec(ctx, `INSERT INTO device_replacements(tenant_id,subscription_id,customer_id,user_id,old_device_id,nas_id,target_mac,challenge_id,status,expires_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7,$8,'CHALLENGE',$9)`, tenantID, c.SubscriptionID, c.CustomerID, userID, c.OldDeviceID, c.NASID, c.TargetMAC, challengeID, expiry)
		if err != nil {
			return fmt.Errorf("create device challenge: %w", err)
		}
		return nil
	})
}

func (s *PostgresStore) ChallengeEmail(ctx context.Context, tenantID, userID, challengeID string) (email string, err error) {
	err = s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT u.email::text FROM device_replacements r JOIN users u ON u.id=r.user_id AND u.tenant_id=r.tenant_id AND u.email_verified_at IS NOT NULL WHERE r.tenant_id=$1::uuid AND r.user_id=$2::uuid AND r.challenge_id=$3 AND r.status='CHALLENGE' AND r.expires_at>now()`, tenantID, userID, challengeID).Scan(&email)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrReplacementNotEligible
	}
	return email, err
}

func (s *PostgresStore) VerifyChallenge(ctx context.Context, tenantID, userID, challengeID string) error {
	return s.db.InTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var subID, customerID, oldID, targetMAC, nasID, replacementID string
		var quota *int64
		err := tx.QueryRow(ctx, `SELECT r.id::text,r.subscription_id::text,r.customer_id::text,r.old_device_id::text,r.target_mac,r.nas_id::text FROM device_replacements r WHERE r.tenant_id=$1::uuid AND r.user_id=$2::uuid AND r.challenge_id=$3 AND r.status='CHALLENGE' AND r.expires_at>now()`, tenantID, userID, challengeID).Scan(&replacementID, &subID, &customerID, &oldID, &targetMAC, &nasID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrReplacementNotEligible
		}
		if err != nil {
			return err
		}
		var currentID string
		err = tx.QueryRow(ctx, `SELECT s.device_id::text,p.quota_bytes FROM subscriptions s JOIN plans p ON p.id=s.plan_id AND p.tenant_id=s.tenant_id AND p.status='ACTIVE' JOIN customers c ON c.id=s.customer_id AND c.tenant_id=s.tenant_id AND c.user_id=$3::uuid AND c.status='ACTIVE' JOIN nas n ON n.id=$4::uuid AND n.tenant_id=s.tenant_id AND n.status='ACTIVE' WHERE s.tenant_id=$1::uuid AND s.id=$2::uuid AND s.customer_id=$5::uuid AND s.status='ACTIVE' AND s.starts_at<=now() AND s.expires_at>now() FOR UPDATE OF s`, tenantID, subID, userID, nasID, customerID).Scan(&currentID, &quota)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrReplacementNotEligible
		}
		if err != nil {
			return err
		}
		if currentID != oldID {
			return ErrReplacementNotEligible
		}
		var open bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE tenant_id=$1::uuid AND subscription_id=$2::uuid AND status<>'CLOSED') OR subscription_has_radius_reservation($1::uuid,$2::uuid)`, tenantID, subID).Scan(&open); err != nil {
			return err
		}
		if open {
			return ErrReplacementNotEligible
		}
		if quota != nil {
			var remaining bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM usage_counters WHERE tenant_id=$1::uuid AND subscription_id=$2::uuid AND period_start<=now() AND period_end>now() AND exhausted_at IS NULL AND consumed_bytes<quota_bytes)`, tenantID, subID).Scan(&remaining); err != nil {
				return err
			}
			if !remaining {
				return ErrReplacementNotEligible
			}
		}
		var targetID, owner, status string
		err = tx.QueryRow(ctx, `SELECT id::text,customer_id::text,status FROM devices WHERE tenant_id=$1::uuid AND normalized_mac=$2 AND status<>'REMOVED' FOR UPDATE`, tenantID, targetMAC).Scan(&targetID, &owner, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `INSERT INTO devices(tenant_id,customer_id,mac_address,normalized_mac) VALUES ($1::uuid,$2::uuid,$3,$3) RETURNING id::text`, tenantID, customerID, targetMAC).Scan(&targetID)
		} else if err == nil && (owner != customerID || status != "ACTIVE") {
			return ErrReplacementNotEligible
		}
		if err != nil {
			return fmt.Errorf("resolve replacement target: %w", err)
		}
		_, err = tx.Exec(ctx, `UPDATE device_replacements SET target_device_id=$4::uuid,status='PENDING',verified_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='CHALLENGE' AND target_mac=$3`, tenantID, replacementID, targetMAC, targetID)
		return err
	})
}
