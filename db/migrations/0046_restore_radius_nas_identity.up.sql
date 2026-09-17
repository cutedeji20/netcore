-- RouterOS sends the WireGuard source address as NAS-IP-Address when its
-- RADIUS client uses that tunnel source. hotspot_address remains the local
-- captive-portal redirect address; nasname is the RADIUS request identity.

BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM nas AS source_nas
          JOIN nas AS existing_nas
            ON existing_nas.nasname = source_nas.radius_source_ip
           AND existing_nas.id <> source_nas.id
         WHERE source_nas.nasname <> source_nas.radius_source_ip
    ) THEN
        RAISE EXCEPTION 'cannot restore RADIUS NAS identity: source address collides with an existing NAS address';
    END IF;
END;
$$;

UPDATE nas
   SET nasname = radius_source_ip
 WHERE nasname <> radius_source_ip;

COMMIT;
