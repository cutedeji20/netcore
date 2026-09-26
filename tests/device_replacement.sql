-- Run against a disposable database after migrations 0001..0056. Every
-- fixture and authorization attempt is rolled back.
BEGIN;
INSERT INTO tenants(id,name,slug,currency) VALUES ('a1000000-0000-4000-8000-000000000001','Replacement test','replacement-test','NGN');
INSERT INTO users(id,tenant_id,email,password_hash,email_verified_at)
VALUES ('a1000000-0000-4000-8000-000000000002','a1000000-0000-4000-8000-000000000001','test@example.test','unused',now());
INSERT INTO customers(id,tenant_id,user_id,customer_number)
VALUES ('a1000000-0000-4000-8000-000000000003','a1000000-0000-4000-8000-000000000001','a1000000-0000-4000-8000-000000000002','R1');
INSERT INTO plans(id,tenant_id,name,price_minor,currency,duration_seconds,download_bps,upload_bps,quota_bytes,quota_exhausted_action)
VALUES ('a1000000-0000-4000-8000-000000000004','a1000000-0000-4000-8000-000000000001','Test plan',100,'NGN',86400,1000000,1000000,100,'DISCONNECT');
INSERT INTO devices(id,tenant_id,customer_id,mac_address,normalized_mac)
VALUES ('a1000000-0000-4000-8000-000000000005','a1000000-0000-4000-8000-000000000001','a1000000-0000-4000-8000-000000000003','aabbccddeeff','aabbccddeeff'),
       ('a1000000-0000-4000-8000-000000000006','a1000000-0000-4000-8000-000000000001','a1000000-0000-4000-8000-000000000003','112233445566','112233445566');
INSERT INTO nas(id,tenant_id,nasname,shortname,secret_ref,radius_source_ip,hotspot_address)
VALUES ('a1000000-0000-4000-8000-000000000007','a1000000-0000-4000-8000-000000000001','10.0.0.1','test','test/secret','10.0.0.1','192.168.88.1');
INSERT INTO subscriptions(id,tenant_id,customer_id,plan_id,device_id,status,starts_at,expires_at,payment_status)
VALUES ('a1000000-0000-4000-8000-000000000008','a1000000-0000-4000-8000-000000000001','a1000000-0000-4000-8000-000000000003','a1000000-0000-4000-8000-000000000004','a1000000-0000-4000-8000-000000000005','ACTIVE',now()-interval '1 hour',now()+interval '1 day','PAID');
INSERT INTO usage_counters(tenant_id,subscription_id,customer_id,period_start,period_end,quota_bytes,consumed_bytes)
VALUES ('a1000000-0000-4000-8000-000000000001','a1000000-0000-4000-8000-000000000008','a1000000-0000-4000-8000-000000000003',now()-interval '1 hour',now()+interval '1 day',100,100);
INSERT INTO device_replacements(tenant_id,subscription_id,customer_id,user_id,old_device_id,target_device_id,nas_id,target_mac,challenge_id,status,expires_at,verified_at)
VALUES ('a1000000-0000-4000-8000-000000000001','a1000000-0000-4000-8000-000000000008','a1000000-0000-4000-8000-000000000003','a1000000-0000-4000-8000-000000000002','a1000000-0000-4000-8000-000000000005','a1000000-0000-4000-8000-000000000006','a1000000-0000-4000-8000-000000000007','112233445566','test-challenge','PENDING',now()+interval '5 minutes',now());
INSERT INTO portal_handoffs(tenant_id,subscription_id,nas_id,user_id,client_mac,nonce_hash,expires_at)
VALUES ('a1000000-0000-4000-8000-000000000001','a1000000-0000-4000-8000-000000000008','a1000000-0000-4000-8000-000000000007','a1000000-0000-4000-8000-000000000002','112233445566',digest(repeat('a',43),'sha256'),now()+interval '100 seconds');
INSERT INTO customers(id,tenant_id,customer_number)
VALUES ('a1000000-0000-4000-8000-000000000009','a1000000-0000-4000-8000-000000000001','FOREIGN');
DO $$ BEGIN
  BEGIN
    INSERT INTO device_replacements(tenant_id,subscription_id,customer_id,user_id,old_device_id,target_device_id,nas_id,target_mac,challenge_id,status,expires_at,verified_at)
    VALUES ('a1000000-0000-4000-8000-000000000001','a1000000-0000-4000-8000-000000000008','a1000000-0000-4000-8000-000000000003','a1000000-0000-4000-8000-000000000002','a1000000-0000-4000-8000-000000000005','a1000000-0000-4000-8000-000000000006','a1000000-0000-4000-8000-000000000007','112233445566','duplicate','PENDING',now()+interval '5 minutes',now());
    RAISE EXCEPTION 'second pending replacement was allowed';
  EXCEPTION WHEN unique_violation THEN NULL;
  END;
END $$;

SET LOCAL app.tenant_id = 'a1000000-0000-4000-8000-000000000001';
SET LOCAL ROLE netcore_app_rw;
DO $$ BEGIN
  IF subscription_has_radius_reservation('a1000000-0000-4000-8000-000000000001','a1000000-0000-4000-8000-000000000008') THEN
    RAISE EXCEPTION 'unexpected RADIUS reservation';
  END IF;
  IF subscription_has_radius_reservation('a1000000-0000-4000-8000-000000000099','a1000000-0000-4000-8000-000000000008') THEN
    RAISE EXCEPTION 'reservation helper crossed tenant boundary';
  END IF;
END $$;
RESET ROLE;

SET LOCAL ROLE netcore_radius;
DO $$
DECLARE n integer;
BEGIN
  SELECT count(*) INTO n FROM radius_portal_handoff_authorize(repeat('a',43),'10.0.0.1','AA:BB:CC:DD:EE:FF');
  IF n<>0 THEN RAISE EXCEPTION 'browser or wrong MAC activated transfer'; END IF;
  SELECT count(*) INTO n FROM radius_portal_handoff_authorize(repeat('a',43),'10.0.0.2','11:22:33:44:55:66');
  IF n<>0 THEN RAISE EXCEPTION 'wrong NAS activated transfer'; END IF;
  SELECT count(*) INTO n FROM radius_portal_handoff_authorize(repeat('a',43),'10.0.0.1','11:22:33:44:55:66');
  IF n<>0 THEN RAISE EXCEPTION 'exhausted quota activated transfer'; END IF;
END $$;
RESET ROLE;
UPDATE devices SET customer_id='a1000000-0000-4000-8000-000000000009' WHERE id='a1000000-0000-4000-8000-000000000006';
SET LOCAL ROLE netcore_radius;
DO $$ DECLARE n integer; BEGIN
  SELECT count(*) INTO n FROM radius_portal_handoff_authorize(repeat('a',43),'10.0.0.1','11:22:33:44:55:66');
  IF n<>0 THEN RAISE EXCEPTION 'foreign-owned target activated transfer'; END IF;
END $$;
RESET ROLE;
UPDATE devices SET customer_id='a1000000-0000-4000-8000-000000000003' WHERE id='a1000000-0000-4000-8000-000000000006';
UPDATE device_replacements SET created_at=now()-interval '7 minutes',expires_at=now()-interval '1 minute' WHERE challenge_id='test-challenge';
SET LOCAL ROLE netcore_radius;
DO $$ DECLARE n integer; BEGIN
  SELECT count(*) INTO n FROM radius_portal_handoff_authorize(repeat('a',43),'10.0.0.1','11:22:33:44:55:66');
  IF n<>0 THEN RAISE EXCEPTION 'expired pending request activated transfer'; END IF;
END $$;
RESET ROLE;
UPDATE device_replacements SET created_at=now(),expires_at=now()+interval '5 minutes' WHERE challenge_id='test-challenge';
DO $$ DECLARE bound uuid; BEGIN
  SELECT device_id INTO bound FROM subscriptions WHERE id='a1000000-0000-4000-8000-000000000008';
  IF bound<>'a1000000-0000-4000-8000-000000000005' THEN RAISE EXCEPTION 'failed attempt changed binding'; END IF;
END $$;
UPDATE usage_counters SET consumed_bytes=0 WHERE subscription_id='a1000000-0000-4000-8000-000000000008';
INSERT INTO sessions(tenant_id,customer_id,subscription_id,device_id,acct_session_id,acct_unique_id,nas_ip_address,normalized_mac)
VALUES ('a1000000-0000-4000-8000-000000000001','a1000000-0000-4000-8000-000000000003','a1000000-0000-4000-8000-000000000008','a1000000-0000-4000-8000-000000000005','old','old-unique','10.0.0.1','aabbccddeeff');
DO $$ DECLARE n integer; BEGIN
  SELECT count(*) INTO n FROM radius_portal_handoff_authorize(repeat('a',43),'10.0.0.1','11:22:33:44:55:66');
  IF n<>0 THEN RAISE EXCEPTION 'open session activated transfer'; END IF;
END $$;
DELETE FROM sessions WHERE acct_unique_id='old-unique';
SET LOCAL ROLE netcore_radius;
DO $$ DECLARE n integer; BEGIN
  SELECT count(*) INTO n FROM radius_portal_handoff_authorize(repeat('a',43),'10.0.0.1','11:22:33:44:55:66');
  IF n<>1 THEN RAISE EXCEPTION 'verified observed MAC did not activate transfer'; END IF;
  SELECT count(*) INTO n FROM radius_portal_handoff_authorize(repeat('a',43),'10.0.0.1','11:22:33:44:55:66');
  IF n<>0 THEN RAISE EXCEPTION 'handoff replay accepted'; END IF;
  SELECT count(*) INTO n FROM radius_device_mac_authorize('10.0.0.1','AA:BB:CC:DD:EE:FF');
  IF n<>0 THEN RAISE EXCEPTION 'old MAC auto-login still accepted'; END IF;
END $$;
RESET ROLE;
DO $$ DECLARE bound uuid; state text; paid text; expiry timestamptz; used bigint; BEGIN
  SELECT device_id,payment_status,expires_at INTO bound,paid,expiry FROM subscriptions WHERE id='a1000000-0000-4000-8000-000000000008';
  IF bound<>'a1000000-0000-4000-8000-000000000006' THEN RAISE EXCEPTION 'binding not switched'; END IF;
  IF paid<>'PAID' OR expiry<now()+interval '23 hours' THEN RAISE EXCEPTION 'payment or expiry changed'; END IF;
  SELECT consumed_bytes INTO used FROM usage_counters WHERE subscription_id='a1000000-0000-4000-8000-000000000008';
  IF used<>0 THEN RAISE EXCEPTION 'quota usage changed'; END IF;
  SELECT status INTO state FROM device_replacements WHERE challenge_id='test-challenge';
  IF state<>'CONSUMED' THEN RAISE EXCEPTION 'pending request not consumed'; END IF;
END $$;
DELETE FROM radius_access_reservations WHERE subscription_id='a1000000-0000-4000-8000-000000000008';
SET LOCAL ROLE netcore_radius;
DO $$ DECLARE n integer; BEGIN
  SELECT count(*) INTO n FROM radius_device_mac_authorize('10.0.0.1','11:22:33:44:55:66');
  IF n<>1 THEN RAISE EXCEPTION 'new MAC auto-login not accepted'; END IF;
END $$;
RESET ROLE;
-- A newer exhausted plan on the same MAC must not mask the still-usable plan.
DELETE FROM radius_access_reservations WHERE subscription_id='a1000000-0000-4000-8000-000000000008';
INSERT INTO subscriptions(id,tenant_id,customer_id,plan_id,device_id,status,starts_at,expires_at,payment_status)
VALUES ('a1000000-0000-4000-8000-000000000010','a1000000-0000-4000-8000-000000000001','a1000000-0000-4000-8000-000000000003','a1000000-0000-4000-8000-000000000004','a1000000-0000-4000-8000-000000000006','ACTIVE',now()-interval '1 hour',now()+interval '2 days','PAID');
INSERT INTO usage_counters(tenant_id,subscription_id,customer_id,period_start,period_end,quota_bytes,consumed_bytes,exhausted_at)
VALUES ('a1000000-0000-4000-8000-000000000001','a1000000-0000-4000-8000-000000000010','a1000000-0000-4000-8000-000000000003',now()-interval '1 hour',now()+interval '2 days',100,100,now());
SET LOCAL ROLE netcore_radius;
DO $$ DECLARE chosen uuid; BEGIN
  SELECT subscription_id INTO chosen FROM radius_device_mac_authorize('10.0.0.1','11:22:33:44:55:66');
  IF chosen IS DISTINCT FROM 'a1000000-0000-4000-8000-000000000008'::uuid THEN RAISE EXCEPTION 'exhausted newer plan masked eligible plan'; END IF;
END $$;
RESET ROLE;
DELETE FROM radius_access_reservations WHERE subscription_id='a1000000-0000-4000-8000-000000000008';
UPDATE customers SET status='SUSPENDED' WHERE id='a1000000-0000-4000-8000-000000000003';
SET LOCAL ROLE netcore_radius;
DO $$ DECLARE n integer; BEGIN
  SELECT count(*) INTO n FROM radius_device_mac_authorize('10.0.0.1','11:22:33:44:55:66');
  IF n<>0 THEN RAISE EXCEPTION 'suspended customer auto-connected'; END IF;
END $$;
RESET ROLE;
UPDATE customers SET status='ACTIVE' WHERE id='a1000000-0000-4000-8000-000000000003';
UPDATE plans SET status='RETIRED' WHERE id='a1000000-0000-4000-8000-000000000004';
SET LOCAL ROLE netcore_radius;
DO $$ DECLARE chosen uuid; BEGIN
  SELECT subscription_id INTO chosen FROM radius_device_mac_authorize('10.0.0.1','11:22:33:44:55:66');
  IF chosen IS DISTINCT FROM 'a1000000-0000-4000-8000-000000000008'::uuid THEN RAISE EXCEPTION 'retired plan broke active entitlement'; END IF;
END $$;
RESET ROLE;
DELETE FROM radius_access_reservations WHERE subscription_id='a1000000-0000-4000-8000-000000000008';
SET LOCAL ROLE netcore_radius;
DO $$ DECLARE n integer; BEGIN
  SELECT count(*) INTO n FROM radius_device_mac_authorize('10.0.0.2','11:22:33:44:55:66');
  IF n<>0 THEN RAISE EXCEPTION 'unknown NAS auto-connected device'; END IF;
  SELECT count(*) INTO n FROM radius_device_mac_authorize('10.0.0.1','AA:BB:CC:DD:EE:FF');
  IF n<>0 THEN RAISE EXCEPTION 'former MAC auto-connected after replacement'; END IF;
END $$;
RESET ROLE;
UPDATE usage_counters SET consumed_bytes=100, exhausted_at=now() WHERE subscription_id='a1000000-0000-4000-8000-000000000008';
SET LOCAL ROLE netcore_radius;
DO $$ DECLARE n integer; BEGIN
  SELECT count(*) INTO n FROM radius_device_mac_authorize('10.0.0.1','11:22:33:44:55:66');
  IF n<>0 THEN RAISE EXCEPTION 'exhausted DISCONNECT plans auto-connected'; END IF;
END $$;
RESET ROLE;
UPDATE usage_counters SET consumed_bytes=0, exhausted_at=NULL WHERE subscription_id='a1000000-0000-4000-8000-000000000008';
UPDATE subscriptions SET expires_at=now()-interval '1 second',status='EXPIRED' WHERE id='a1000000-0000-4000-8000-000000000008';
SET LOCAL ROLE netcore_radius;
DO $$ DECLARE n integer; BEGIN
  SELECT count(*) INTO n FROM radius_device_mac_authorize('10.0.0.1','11:22:33:44:55:66');
  IF n<>0 THEN RAISE EXCEPTION 'expired plan auto-connected'; END IF;
END $$;
RESET ROLE;
UPDATE subscriptions SET expires_at=now()+interval '1 day',status='ACTIVE' WHERE id='a1000000-0000-4000-8000-000000000008';
INSERT INTO sessions(tenant_id,customer_id,subscription_id,device_id,acct_session_id,acct_unique_id,nas_ip_address,normalized_mac)
VALUES ('a1000000-0000-4000-8000-000000000001','a1000000-0000-4000-8000-000000000003','a1000000-0000-4000-8000-000000000008','a1000000-0000-4000-8000-000000000006','mac-old','mac-old-unique','10.0.0.1','112233445566');
SET LOCAL ROLE netcore_radius;
DO $$ DECLARE n integer; BEGIN
  SELECT count(*) INTO n FROM radius_device_mac_authorize('10.0.0.1','11:22:33:44:55:66');
  IF n<>0 THEN RAISE EXCEPTION 'concurrent session limit bypassed'; END IF;
END $$;
RESET ROLE;
ROLLBACK;
