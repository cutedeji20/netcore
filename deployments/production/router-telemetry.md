# Router dashboard telemetry

Migration `0066` builds historical and ongoing HotSpot byte totals from accepted RADIUS accounting. It does not require RouterOS REST access. It counts only the increase in each accounting session's high-water counters, so retransmissions and older packets do not inflate usage.

WAN totals and observed HotSpot host counts require the optional worker collector. WAN totals start with the first two successful samples; traffic before polling began is unavailable. Counter resets and polling gaps may undercount. The dashboard labels these figures separately from customer HotSpot usage. Active devices come from fresh RADIUS sessions (last accounting update within 10 minutes); observed hosts come from the router and are shown only while the latest sample is under three minutes old.

1. On each MikroTik, create a dedicated user group with `read,rest-api` policies, a user in that group, and enable `www-ssl` with a certificate whose IP SAN matches the router's private management address. Restrict the service and firewall to the worker's private source address. Do not enable plain HTTP for production telemetry.
2. Confirm the router exists in NetCore and note its router ID and management IP. Confirm the worker can reach `https://<management-ip>/rest/interface/print` and that the intended WAN interface reports `rx-byte` and `tx-byte`.
3. In `${NETCORE_RUNTIME_DIR}/app`, create separate readable files for the RouterOS password and its issuing CA certificate. Do not put the password in `.env`, the database, or Git.
4. Create `${NETCORE_RUNTIME_DIR}/app/router-telemetry.json` with one target per router:

   ```json
   [
     {
       "router_id": "<NetCore router UUID>",
       "url": "https://172.16.0.4",
       "wan_interface": "ether1",
       "username": "netcore-telemetry",
       "password_file": "/run/netcore/runtime/router-password",
       "ca_cert_file": "/run/netcore/runtime/router-ca.pem"
     }
   ]
   ```

5. Set `NETCORE_ROUTER_TELEMETRY_CONFIG_FILE=/run/netcore/runtime/router-telemetry.json` in the production Compose `.env`, then recreate only the worker. Leave it empty to keep polling disabled. The worker validates private HTTPS, the matching NetCore router management IP, the TLS certificate, and the configured interface before storing a sample. It polls once a minute. Check worker logs for `router telemetry polling failed` and verify the dashboard sample time advances.

Apply migration `0066` before restarting API and worker. Its maintenance-window lock holds accounting writes until historical backfill and trigger creation commit. Keep the RADIUS writer's durable spool available, check free disk and spool capacity first, and confirm `radius-replay` drains the queued accounting afterward. Do not start the new API before the migration succeeds. No voucher Key Vault setting is part of this work.
