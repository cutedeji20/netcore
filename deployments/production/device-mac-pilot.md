# Two-AP MAC auto-connect pilot

This is an operator-run physical acceptance test, not a deployment script. Keep
`NETCORE_DEVICE_REPLACEMENT_ENABLED=false` unless separately piloting verified
device replacement. MAC auto-login applies only to a subscription already bound
to the MAC observed by the MikroTik; it never identifies two different MACs as
one phone.

## Before changing RouterOS

1. Confirm a recent restorable database backup and healthy API, PostgreSQL,
   FreeRADIUS, and accounting replay services. Apply migrations through 0056
   before the pilot. Confirm the RADIUS MAC password file is installed through
   the existing runtime secret mechanism; do not print or paste its contents.
2. Use one test customer with an active, unexpired device-bound subscription and
   available quota. Record the subscription ID, expiry, quota consumed, bound
   MAC, and the router's NAS-IP-Address. Do not buy another plan for this test.
3. On the phone, compare the Wi-Fi MAC shown for SSID A and SSID B. They must
   match the subscription's bound MAC for browserless access. Android's
   randomized per-network MAC often differs even on the same physical phone.
   If they differ, stop this pilot; use verified device replacement or a
   deliberate device-MAC setting on the test phone. Never infer identity from
   hostname, IP address, account email, or AP name.
4. Verify both APs bridge client traffic to the same MikroTik HotSpot/NAS, with
   no AP-side DHCP/NAT and no MAC translation. Check the router's DHCP lease
   and HotSpot host MAC for the test phone on each AP.

## Controlled enablement

Use RouterOS Safe Mode. On the *test HotSpot profile only*, add `mac` to the
existing `login-by=http-pap,mac-cookie` methods and set `mac-auth-password` to
the same deployment-managed secret used by FreeRADIUS. Enter the secret in a
secure operator session; never put it in a ticket, screenshot, command transcript,
rendered file committed to Git, or this document. Do not change the RADIUS
shared secret or NAS identity. Save the previous profile settings for rollback.

Disconnect the test phone from SSID A and wait for its old HotSpot session to
close; do not delete other customers' sessions or flush Redis. Connect to SSID B
once. Expected: the router sends MAC authentication to FreeRADIUS, receives
Access-Accept, shows an active RADIUS-backed HotSpot session, and the phone
browses without opening or signing in to the customer portal. Confirm accounting
starts and the same subscription ID, expiry, and consumed quota continue.

Negative checks: an unregistered/different MAC, expired subscription, suspended
customer, exhausted DISCONNECT plan, or unknown NAS must not receive browserless
access. They may still reach the captive portal where applicable. A concurrently
active old session can legitimately deny a new one when the plan allows only one
session; wait for accounting close rather than bypassing the limit.

If any check fails, restore `login-by=http-pap,mac-cookie` and the previous
`mac-auth-password` setting in Safe Mode, then inspect RADIUS/HotSpot logs for
the single test attempt. Do not leave MAC authentication enabled for all users
until the pilot is accepted. MAC addresses can be spoofed, so keep session and
quota controls and assess this trade-off before wider rollout.
