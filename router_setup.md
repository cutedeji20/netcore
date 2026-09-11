# NetCore RouterOS captive-portal setup

This guide connects a **MikroTik RouterOS HotSpot** to the NetCore portal at
`https://hotspot.durabledatahubs.com`. It is a staged onboarding guide: start
with a test SSID, VLAN, or router and promote it to customer traffic only after
every acceptance check passes.

> **Do not enable a customer router yet.** Network & AAA can create a disabled
> router/NAS and issue a one-time protected setup download after password + MFA.
> It does not apply RouterOS configuration or write FreeRADIUS runtime files;
> FreeRADIUS remains disabled in the normal production Compose start until the
> private-path acceptance run succeeds.

## What this creates

```text
Guest device
    |
    v
MikroTik guest HotSpot
    |  unauthenticated HTTP request
    v
local login.html on the router
    |  redirects only to the approved portal host
    v
https://hotspot.durabledatahubs.com/portal.html
    |                         |
    |                         +-- purchase or customer sign-in
    |                                      |
    |                                      v
    |                            Paystack webhook confirms payment
    v
one-use, MAC-and-NAS-bound handoff
    |
    v
RouterOS http-pap login --> private RADIUS path --> FreeRADIUS
                                                   |
                                                   v
                                           active-plan decision
                                                   |
                                      Access-Accept + limits/accounting
```

No payment confirmation means no RADIUS authorisation and no general internet
access. A handoff token is short-lived, tied to the device MAC address and NAS,
and can be used once only. It is not a customer password.

## Security rules that must not be changed

1. Use a **unique RADIUS shared secret per router**. Do not reuse a secret
   across locations.
2. Carry RADIUS only over a private management path: an existing site-to-site
   VPN, or a RouterOS v7 WireGuard tunnel to the Azure VM. Never expose UDP
   1812, 1813, or 3799 to the public internet.
3. Keep the HotSpot profile on `http-pap`. The NetCore portal uses it to return
   the one-use handoff in the RouterOS login request. Changing it to CHAP needs
   a different portal implementation.
4. Allow only `hotspot.durabledatahubs.com` through the unauthenticated
   walled garden. Do not allow `*`, social-media domains, payment-provider
   domains, a broad subnet, or the whole internet.
5. Never place a RADIUS secret, RouterOS administrator password, database
   password, Key Vault value, or rendered `.rsc` script in Git, a browser form,
   chat, screenshots, or a support ticket.
6. Work from a local console or an out-of-band management connection. A HotSpot
   or firewall mistake can otherwise lock you out of the router.

## 1. Confirm the release is ready for a staging router

Run these checks on the Azure VM before touching a router. They confirm the
control plane is healthy; they do **not** enable FreeRADIUS.

```bash
cd /srv/netcore/src
docker compose -f deployments/production/compose.yaml ps
curl -fsS --max-time 15 -o /dev/null -w '%{http_code}\n' \
  https://hotspot.durabledatahubs.com/portal.html
```

The portal check must return `200`, and PostgreSQL, Redis, API, worker, UI, and
Caddy must be healthy/running. Confirm the following separately:

- the Paystack integration is configured in **Settings → Integrations** and its
  webhook has been tested in Paystack's test environment;
- Resend is configured if purchase receipts or staff/customer e-mails are
  expected during the test;
- an administrator can log in with MFA and can create a test plan and customer;
- a database backup and a router backup are available; and
- the router test window has a named operator and a rollback owner.

Do not use real customer payments for the first router test. Use a Paystack test
key and test transaction until the full RADIUS and accounting checks below have
passed.

## 2. Record the values before rendering anything

Fill this worksheet in a secure operator record, not in this repository.

| Value | What to use |
|---|---|
| Portal host | `hotspot.durabledatahubs.com` |
| Portal origin | `https://hotspot.durabledatahubs.com` |
| Portal address | The current, reserved public IPv4 address for the portal host. Confirm it with `nslookup hotspot.durabledatahubs.com` immediately before import. If the Azure public IP is not static, make it static before proceeding. |
| RADIUS address | The NetCore address **inside the management VPN**, not the Azure public IP. |
| NAS source address | The router's VPN/source address seen by FreeRADIUS. It must exactly equal the registered `nas.nasname` value. |
| RADIUS secret | A new, high-entropy secret for this router only, held by the approved secret manager. |
| CoA source address | One NetCore control-plane VPN address as a `/32`; never a guest or office subnet. |
| Guest interface | A dedicated guest bridge or VLAN. Never attach HotSpot to the WAN or a bridge port that also carries your management network. |
| HotSpot profile/user profile | The existing names created for the staging HotSpot server. |
| HotSpot HTML directory | A local RouterOS directory reserved for this HotSpot's pages. |

Create the router and NAS with **Network & AAA → Add router**. The dashboard
stores only the encrypted secret envelope; its one-time setup download is the
only place the plaintext secret is made available. Do not work around this by
manually changing production tables.

## 3. Prepare the router without changing customer traffic

Use a test router or a new guest VLAN/SSID first. MikroTik permits one HotSpot
server per interface; when using a bridge, choose the bridge itself rather than
one of its bridge ports. See the [MikroTik HotSpot documentation](https://help.mikrotik.com/docs/spaces/ROS/pages/56459266/HotSpot%2B-%2BCaptive%2Bportal).

1. Connect through WinBox MAC access, serial console, or another out-of-band
   method. Save both exports before making changes:

   ```routeros
   /export file=pre-netcore-hotspot
   /system backup save name=pre-netcore-hotspot
   ```

   Download both backup files, protect them as confidential configuration, and
   keep them off the guest network.

2. Create the isolated guest VLAN/bridge, DHCP scope, and SSID according to
   the site's existing network design. Verify that a guest device receives an
   address but has no route to the router management LAN.

3. Run RouterOS's guided HotSpot setup **only on that guest bridge/VLAN**. It
   creates the HotSpot server and the profile names needed by NetCore.

   ```routeros
   /ip hotspot setup
   ```

   Keep the setup's temporary local user only long enough to prove that the
   guest HotSpot works. Remove or disable it before promotion: RouterOS checks
   matching local HotSpot users before consulting RADIUS, so a local account
   could become an unintended access bypass.

4. Record the real names and confirm the target is correct:

   ```routeros
   /ip hotspot print detail
   /ip hotspot profile print detail
   /ip hotspot user profile print detail
   /radius print detail
   ```

Do not continue if the guest interface, address pool, or management-network
separation is unclear. Restore the router backup and correct the design first.

## 4. Establish the private RADIUS path

The physical router must be able to reach the Azure VM over a private address.
Use an existing VPN if the router is already connected to the private management
network. Otherwise, create a dedicated RouterOS v7 WireGuard peer or a
site-to-site VPN with these constraints:

- assign separate tunnel addresses for the router and NetCore control plane;
- route only the RADIUS/control-plane addresses through the tunnel;
- restrict the Azure NSG and host firewall to the router's tunnel source;
- keep RouterOS management services unavailable from the guest VLAN; and
- document the tunnel endpoint, peer public keys, allowed addresses, and owner
  in the secure network record.

The RADIUS service ports are UDP 1812 (authentication) and UDP 1813
(accounting). RouterOS supports RADIUS accounting, interim updates, and CoA
attributes; [MikroTik's RADIUS reference](https://help.mikrotik.com/docs/spaces/ROS/pages/328097/RADIUS)
describes the expected accounting packet fields and supported authorisation
attributes.

Before enabling the service, prove the private route from the router and ensure
there is **no** Azure public firewall/NSG rule for these UDP ports. The expected
path is:

```text
router VPN source IP --> VM private/VPN address --> Docker FreeRADIUS service
```

It is not:

```text
public internet --> Azure public IP --> UDP 1812/1813/3799
```

## 5. Register the NAS and render FreeRADIUS configuration

This is the release gate that is not yet available in the dashboard.

An authorised infrastructure operator must, through the forthcoming Network &
AAA onboarding workflow or an already-reviewed maintenance process:

1. create the site, router, and active NAS record for the tenant;
2. set the NAS source address exactly to the router VPN/source IP;
3. create one RADIUS shared secret in the secret manager and store only its
   reference against the router/NAS; and
4. render `freeradius/clients.conf.template` into the Azure runtime directory.

The rendered runtime file is:

```text
/srv/netcore/runtime/radius/clients.conf
```

It must be owned by the FreeRADIUS UID/GID `101:101` and have mode `0400`. It
is secret-bearing and must never be committed. Do not try to derive its
`secret` from `nas.secret_ref`.

On the Azure VM, validate the exact image/configuration before starting the
services:

```bash
cd /srv/netcore/src
docker compose -f deployments/production/compose.yaml --profile radius build radius radius-replay
docker compose -f deployments/production/compose.yaml --profile radius run --rm -e NETCORE_RADIUS_VALIDATE=1 radius
docker compose -f deployments/production/compose.yaml --profile radius run --rm -e NETCORE_RADIUS_VALIDATE=1 radius-replay
```

Start the staging services only after both validation commands succeed:

```bash
docker compose -f deployments/production/compose.yaml --profile radius up -d radius radius-replay
```

The writer must journal accounting traffic to the dedicated spool before it
acknowledges it. The replay service must later apply those records exactly once.
The full durable-spool checks are in
[`deployments/production/README.md`](deployments/production/README.md).

## 6. Render the RouterOS assets securely

NetCore supplies templates in [`routeros/hotspot`](routeros/hotspot). They are
not safe to import unchanged.

| Template | Rendered router file |
|---|---|
| `provision-hotspot.rsc.tmpl` | A one-time secret-bearing `.rsc` file |
| `login.html.tmpl` | `login.html` |
| `flogin.html.tmpl` | `flogin.html` |
| `error.html.tmpl` | `error.html` |
| `maintenance.html.tmpl` | A temporary replacement `login.html` during planned portal work |

Replace every placeholder in the provisioning template from the secure
worksheet:

```text
__RADIUS_ADDRESS__
__RADIUS_SHARED_SECRET__
__COA_SOURCE_ADDRESS__
__PORTAL_HOST__
__PORTAL_ADDRESS__
__HOTSPOT_PROFILE__
__HOTSPOT_USER_PROFILE__
__HOTSPOT_HTML_DIR__
```

Replace `__PORTAL_ORIGIN__` in the HTML templates with:

```text
https://hotspot.durabledatahubs.com
```

On the secure rendering workstation, check that no `__PLACEHOLDER__` text
remains. Review the output without copying the secret into any record. Keep
the rendered `.rsc` encrypted until import, then delete that temporary copy
according to the secret-handling procedure.

The provisioning script intentionally does all of the following:

- enables RADIUS and RouterOS-selected interim accounting updates;
- configures a one-minute MAC cookie for a brief reconnect only;
- configures the `hotspot` RADIUS client with the unique secret;
- enables incoming CoA/Disconnect on UDP 3799 only from the exact NetCore
  control-plane `/32` and drops every other source;
- allows the portal host, and no broader internet destination, before login;
- applies a portal-only firewall state when a quota policy returns the
  `netcore-quota-exhausted` filter; and
- keeps the redirect and failure pages local to the router, with no third-party
  fonts, scripts, images, or analytics.

## 7. Import the configuration on the staging router

Perform this from the out-of-band management session during the planned test
window.

1. Upload the rendered `.rsc` file and the three rendered HTML files to the
   router using WinBox **Files** or a secure management transfer. Place the
   HTML files in the directory selected for `__HOTSPOT_HTML_DIR__`.
2. Put the management session into RouterOS Safe Mode in WinBox, then import
   the rendered script:

   ```routeros
   /import file-name=<rendered-netcore-hotspot-file>.rsc
   ```

3. Verify the expected settings rather than assuming import succeeded:

   ```routeros
   /ip hotspot profile print detail
   /ip hotspot user profile print detail
   /radius print detail
   /radius incoming print detail
   /ip hotspot walled-garden print detail where comment="netcore-portal"
   /ip firewall filter print detail where comment~"netcore-coa|netcore-quota"
   ```

   Confirm that there is exactly one `netcore-portal` walled-garden rule. Do
   not repeatedly import the same script without reviewing that rule list.

4. Delete the rendered secret-bearing `.rsc` file from the router after the
   import and verification. The RouterOS RADIUS configuration retains the
   value; the import file must not remain downloadable. Retain only the
   protected operator copy required by the secret-rotation policy.
5. Leave Safe Mode only after the local management connection and guest VLAN
   still work as expected.

The local `login.html` expands RouterOS values for client MAC, NAS address, and
the local HotSpot login URL, then sends the browser to the portal. It does not
allow the browser to choose a return URL or tenant.

## 8. Staging acceptance checklist

Run the tests below with a test customer and a test plan. Capture the result,
time, router identity, and operator in the release record, but never capture a
secret or handoff token.

### Customer journey

1. Connect a new phone or laptop to the guest SSID. It must receive a guest IP
   and show the NetCore portal rather than general internet access.
2. Confirm `https://hotspot.durabledatahubs.com/portal.html` loads before
   authentication. Other websites must remain blocked.
3. Create a customer account with e-mail and password, select the test plan,
   and complete a Paystack **test** payment.
4. Confirm the webhook marks the payment/subscription correctly before the
   portal asks the router to authorise the device.
5. Confirm the device receives access at the selected rate and is disconnected
   when the returned session timeout, quota, or plan expiry requires it.
6. Disconnect/reconnect the device and confirm the brief MAC-cookie behaviour
   does not outlive the RADIUS session limits.

### Negative and accounting tests

1. Attempt an expired, replayed, wrong-MAC, and wrong-NAS handoff. Each must
   receive Access-Reject and must not create internet access.
2. Verify RADIUS Start, Interim, Stop, duplicate Interim, Accounting-On, and
   Accounting-Off records. A retransmitted Interim must not double-count usage.
3. From the approved NetCore control-plane source, verify CoA/Disconnect can
   reach UDP 3799. From any other source, verify the router drops it.
4. Temporarily isolate PostgreSQL from the replay service, send accounting,
   and prove the durable spool grows while the NAS receives an
   Accounting-Response. Restore the database and prove the spool drains
   without duplicate usage.
5. Test the quota-exhausted experience on the target phone/browser: only the
   portal/renewal path should be reachable. Do not attempt HTTPS interception.
6. Replace `login.html` with the local maintenance page, reconnect a guest,
   and confirm existing customer sessions remain connected. Restore the normal
   page afterwards.

Stop and roll back if any result differs from the checklist. A successful
portal redirect alone is not proof that access control or accounting is safe.

## 9. Promote to a customer router

Promotion requires a documented approval after every staging check above is
green. During the first live window:

1. Keep an operator on the router console and another watching the NetCore,
   FreeRADIUS, and spool/replay logs.
2. Use a small guest SSID/VLAN first, not the full subscriber estate.
3. Confirm the router's NAS source address and secret are the production ones;
   never copy values from the staging router.
4. Watch successful and rejected RADIUS authentication, session/accounting
   creation, interim activity, spool age/free space, and Paystack webhook
   processing.
5. Expand only after a real controlled customer journey, timeout, disconnect,
   and accounting cycle are all correct.

## Rollback

If the portal, authorisation, or accounting path fails:

1. Stop accepting new guest clients on the affected test SSID/VLAN, or restore
   the pre-change HotSpot/profile configuration from the protected router
   backup.
2. If the planned action is only portal maintenance, upload the local
   `maintenance.html` as `login.html` instead; do not leave guests at a browser
   error page.
3. Do not delete database records, accounting spool files, router/NAS records,
   or secret-manager entries while investigating. They are needed for a safe
   recovery and audit trail.
4. Record the router, time, impact, and relevant redacted logs; rotate the
   router's RADIUS secret if there is any chance the rendered script was
   exposed.

## Troubleshooting boundaries

| Symptom | Safe first checks |
|---|---|
| Guest does not reach the portal | Verify the HotSpot is attached to the intended guest bridge/VLAN, `login.html` is in the configured local HTML directory, and the single `netcore-portal` walled-garden rule matches the portal host. |
| Portal loads but router login fails | Check the private VPN route, the active NAS source address, the per-router RADIUS secret, and whether the `radius`/`radius-replay` profile passed validation and is running. Never paste the secret into logs. |
| Login succeeds but internet does not work | Inspect the RADIUS Access-Accept attributes and HotSpot active-session state. Confirm no quota filter or restrictive plan limit was returned unexpectedly. |
| Sessions/usage are missing | Check UDP 1813 through the private path, FreeRADIUS writer health, spool free space/age, and replay status. Do not clear the spool. |
| CoA is reachable from an unapproved host | Treat this as a security incident: disable it, review the router input rules and VPN routing, then rotate the RADIUS secret before retesting. |

## Current admin roadmap

After this guide is accepted for staging, the next dashboard delivery should
add audited, permission-checked write workflows for:

- vouchers: create batches, set expiry/limits, disable and audit them;
- subscriptions: assign, renew, suspend, and resume with explicit reasons;
- workspace and automations: edit/enable with validation and audit history;
- router/NAS onboarding: create site/router/NAS, store only secret references,
  validate connectivity, render reviewed provisioning material, and disable a
  router safely; and
- sessions: a queued, audited Disconnect/CoA action only after the RADIUS
  production acceptance above is complete.

Until then, the dashboard's Network & AAA, vouchers, billing, subscriptions,
sessions, automations, and workspace views should be treated as read-only
operational visibility, not as completed administration tools.

## References

- [NetCore RouterOS deployment templates](routeros/hotspot/README.md)
- [NetCore FreeRADIUS requirements](freeradius/README.md)
- [NetCore production spool/replay gates](deployments/production/README.md)
- [MikroTik HotSpot / captive portal documentation](https://help.mikrotik.com/docs/spaces/ROS/pages/56459266/HotSpot%2B-%2BCaptive%2Bportal)
- [MikroTik RADIUS documentation](https://help.mikrotik.com/docs/spaces/ROS/pages/328097/RADIUS)
- [MikroTik HotSpot customisation documentation](https://help.mikrotik.com/docs/spaces/ROS/pages/87162881/Hotspot%2Bcustomisation)
