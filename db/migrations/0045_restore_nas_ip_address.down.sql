BEGIN;

UPDATE nas
   SET nasname = radius_source_ip
 WHERE nasname <> radius_source_ip;

COMMIT;
