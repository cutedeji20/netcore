# NetCore RB5009 completion checklist

This is the remaining production setup after the WireGuard tunnel was created.
It is deliberately ordered: prove the private path, register the router while disabled, validate RADIUS, then make one small HotSpot/portal test live.

Do not place RADIUS on the public Internet. UDP 1812 and 1813 must stay bound to the VM private address only. Do not paste a RADIUS secret, WireGuard private key, customer password, MFA code, or one-time download content into a shell, Git repository, or ticket.

## Current confirmed state

- The Azure VM private application address is `172.16.0.4`.
- The RB5009 WireGuard tunnel uses `10.254.77.2/30`; the Azure peer is `10.254.77.1/30`.
- The router successfully pinged `10.254.77.1` through WireGuard.
- The dashboard supports **Network & AAA → Add router** and one-time AAA package download.
- RADIUS has not yet been intentionally started and no HotSpot change is required until the RADIUS validation stages pass.

## 1. Remove the duplicate private route and prove the tunnel

Keep the existing WinBox/terminal session in **Safe Mode**. The last router output showed two identical routes for `172.16.0.4/32`. Keep exactly one.

First leave the RouterOS pager with `q`, then inspect the routes:

```routeros
/ip/route/print detail where comment="NetCore private RADIUS only"
```

If the output still lists entries numbered `0` and `1` with the same destination and gateway (`wg-netcore`), remove only the duplicate numbered `1`:

```routeros
/ip/route/remove 1
/ip/route/print detail where comment="NetCore private RADIUS only"
```

Expected: one active route only, with destination `172.16.0.4/32` and gateway `wg-netcore`. If the row numbers differ, do not guess: inspect the list first and remove only the duplicate row.

Then confirm the encrypted peer and tunnel reachability:

```routeros
/interface/wireguard/peers/print detail where name="netcore-azure"
/ping 10.254.77.1 src-address=10.254.77.2 count=3
```

Expected: a recent `last-handshake`, increasing `rx`/`tx`, and three replies to the tunnel address. A failed ICMP ping to `172.16.0.4` alone is not conclusive; the later RADIUS transaction is the actual service test.

On the Azure VM, confirm the same peer is visible:

```bash
sudo wg show wg-netcore
```

Expected: the RB5009 peer has a recent handshake and transfer counters. Stop here if it does not; do not begin RADIUS or HotSpot work until it is healthy.

## 2. Register the disabled router in the dashboard

Open **Network & AAA**, select **Add router**, and enter:

| Dashboard field | Value |
| --- | --- |
| Router name | `DataHub-RB5009` |
| Management IP | `192.168.88.1` |
| NAS IP address | `10.254.77.2` |
| RADIUS source IP | `10.254.77.2` |

Save it **disabled**. The management address is for administration only. The NAS and RADIUS source are the WireGuard address because that is where RADIUS packets originate.

Do not activate the router yet. Activation is the final step after an actual Access-Request and accounting cycle succeeds.

## 3. Generate the one-time AAA package

On the newly registered router, choose **Configure AAA**. Complete the required dashboard password and MFA confirmation, then choose **Download setup**.

This action rotates the unique shared secret and provides its only intended plaintext copy. Store the package temporarily in an encrypted operator location. Do not open it in a cloud editor or paste its contents into chat.

The package contains two separate artifacts:

1. A FreeRADIUS `client` block for the Azure runtime file.
2. A RouterOS command/import value that includes the same per-router secret.

Use the package exactly as generated. Do not substitute the Azure public IP, the old L2TP address, or a router-management address for the RADIUS source.

## 4. Install the rendered FreeRADIUS client file on Azure

The downloaded client block is secret-bearing. On the Azure VM, create or replace only the runtime client file using a secure editor/session, then set the required permissions:

```bash
sudo install -d -o 101 -g 101 -m 0700 /srv/netcore/runtime/radius
sudo chown 101:101 /srv/netcore/runtime/radius/clients.conf
sudo chmod 0400 /srv/netcore/runtime/radius/clients.conf
sudo stat -c '%n %U:%G %a' /srv/netcore/runtime/radius/clients.conf
```

Expected final line: owner/group `101:101` and mode `400`. Do not use `cat`, `grep`, screenshots, or Git to inspect the secret-bearing file after writing it. Confirm only metadata with `stat`.

Confirm the deployment bind addresses and runtime locations without printing secrets:

```bash
cd /srv/netcore/src
grep -nE '^NETCORE_(RADIUS_SERVER_ADDRESS|RUNTIME_DIR|RADIUS_SPOOL_DIR)=' deployments/production/.env
```

Expected: `NETCORE_RADIUS_SERVER_ADDRESS=172.16.0.4`. The production compose file binds UDP 1812 and 1813 only to that address and mounts the client file read-only into both RADIUS services.

## 5. Validate the exact RADIUS services before receiving router traffic

Check that the configured spool filesystem has enough free capacity. Do not clear a non-empty spool to make a validation pass.

```bash
cd /srv/netcore/src
grep -nE '^NETCORE_RADIUS_(SPOOL_DIR|SPOOL_MAX_BYTES|SPOOL_MIN_FREE_BYTES)=' deployments/production/.env
docker compose -f deployments/production/compose.yaml --profile radius build radius radius-replay
docker compose -f deployments/production/compose.yaml --profile radius run --rm -e NETCORE_RADIUS_VALIDATE=1 radius
docker compose -f deployments/production/compose.yaml --profile radius run --rm -e NETCORE_RADIUS_VALIDATE=1 radius-replay
```

Proceed only if both validation commands exit successfully. Then start the opt-in RADIUS pair:

```bash
docker compose -f deployments/production/compose.yaml --profile radius up -d radius radius-replay
docker compose -f deployments/production/compose.yaml --profile radius ps
```

Expected: `radius-spool-init` completes successfully; `radius` and `radius-replay` are running. The normal API, UI, worker, database, Redis, and Caddy services should remain healthy.

Verify that RADIUS is bound privately, not publicly:

```bash
sudo ss -lunp | grep -E '172\\.16\\.0\\.4:(1812|1813)'
```

Expected: UDP 1812 and 1813 listen on `172.16.0.4`, never on `0.0.0.0` or the Azure public IP. Keep UDP 3799 closed until a separate approved CoA rollout.

## 6. Apply only the generated RouterOS RADIUS configuration

Still in RouterOS Safe Mode, use only the RADIUS configuration supplied by the one-time package. It must point to `172.16.0.4` and bind its source to `10.254.77.2`.

Before applying it, save a redacted review of existing RADIUS settings:

```routeros
/radius/print detail
/radius/incoming/print detail
```

Apply the generated command or import the generated file through WinBox **Files**. Do not manually type the shared secret. Then confirm the non-secret settings only:

```routeros
/radius/print detail
```

Expected:

- server address is `172.16.0.4`;
- source address is `10.254.77.2`;
- service includes `hotspot` only when the HotSpot test is ready; and
- no public address is used.

Delete the secret-bearing generated import file from the router's **Files** after the import has been verified. RouterOS retains the configured secret.

## 7. Perform a controlled RADIUS acceptance test

Use one test plan and one test customer first. Do not expose the production guest SSID to all customers yet.

1. Configure or select an isolated test guest VLAN/SSID. It must not share the router management bridge or staff network.
2. Install the rendered NetCore `login.html`, `flogin.html`, and `error.html` in the selected HotSpot HTML directory. The portal origin is `https://hotspot.durabledatahubs.com`.
3. Import the generated HotSpot provisioning script only after reviewing its target bridge, address pool, HotSpot profile, RADIUS source, and portal host. Never import it onto WAN or a management bridge.
4. Connect one test phone/laptop to the isolated SSID.
5. Confirm it receives a guest IP, opens `portal.html`, and cannot browse the general Internet before authorisation.
6. Sign in using an existing active test subscription. Paystack is not needed for this test if the subscription already exists.
7. Confirm the router sends an Access-Request, NetCore returns the expected result, and the device gets the plan access limits.
8. Disconnect/reconnect and confirm the router emits Accounting Start, Interim, and Stop. Check the writer/replay logs and spool behaviour.

Useful safe service observation commands on Azure:

```bash
cd /srv/netcore/src
docker compose -f deployments/production/compose.yaml logs --since 10m radius radius-replay
docker compose -f deployments/production/compose.yaml --profile radius ps
```

For full router HotSpot preparation, use sections 7 through 11 of [`router-web-config.md`](router-web-config.md). That guide contains the guest network isolation, portal redirect, handoff, accounting, and negative-test requirements.

## 8. Confirm and activate the router in the dashboard

After one end-to-end test passes, return to **Network & AAA**:

1. Mark the test as confirmed, completing password + MFA verification if prompted.
2. Activate the router only after the dashboard shows the expected RADIUS source and the physical test completed.
3. Record the test time, router name, administrator, and release version; do not record a secret, token, or password.

Before expanding to normal customers, test these failures explicitly:

- expired handoff is rejected;
- replayed handoff is rejected;
- wrong device MAC is rejected;
- wrong NAS is rejected;
- duplicate accounting interim does not double-count usage; and
- public UDP 1812/1813 remain unreachable.

## 9. Production expansion

Use a small guest VLAN/SSID and a scheduled window first. Keep one operator on WinBox/out-of-band access and one watching the Azure services. Expand only after a complete customer access, expiry/disconnect, and accounting cycle is verified.

The paid-login journey requires a working subscription/payment provider when a new customer must purchase access. Since Paystack configuration is deferred, use an already active test subscription for the first RADIUS/HotSpot proof. Do not advertise payment-gated access until Paystack and its webhook verification are separately configured and tested.

## Rollback

If the tunnel, RADIUS, portal, accounting, or guest isolation test fails:

1. Stop new clients joining the affected test SSID/VLAN, or restore the protected pre-change HotSpot configuration.
2. Keep the WireGuard interface/peer but disable the new RADIUS/HotSpot rule first; do not delete tunnel keys or database/audit records during diagnosis.
3. On Azure, stop only the opt-in RADIUS pair if needed:

   ```bash
   cd /srv/netcore/src
   docker compose -f deployments/production/compose.yaml --profile radius stop radius radius-replay
   ```

4. Preserve the accounting spool, redacted logs, and the router backup.
5. If the AAA package or RouterOS import might have been exposed, rotate the router secret in the dashboard, re-render the client file and router package, and repeat validation.

Do not remove the WireGuard route unless you have confirmed it is not used by any other approved private control-plane service.
