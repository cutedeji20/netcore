"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const account = require("./portal-account.js");

test("builds a customer-only account display model from the portal response", () => {
  const value = account.displayModel({
    data: {
      customer_id: "must-not-reach-the-page",
      subscriptions: [{
        id: "11111111-1111-4111-8111-111111111111",
        plan_name: "Weekly access",
		device_label: "Phone A",
		device_mac: "aabbccddeeff",
        metered: true,
        remaining_bytes: 500,
        status: "ACTIVE",
        payment_status: "PAID",
        starts_at: "2026-08-23T12:00:00Z",
        expires_at: "2026-08-30T12:00:00Z",
        tenant_id: "must-not-reach-the-page"
      }],
      payments: [{
        reference: "pay-0123456789abcdef0123456789abcdef",
        amount_minor: 250000,
        currency: "NGN",
        status: "SUCCESS",
        created_at: "2026-08-23T12:00:00Z",
        gateway: "paystack"
      }]
    }
  });

  assert.deepEqual(value, {
    subscriptions: [{
      id: "11111111-1111-4111-8111-111111111111",
      planName: "Weekly access",
		deviceLabel: "Phone A",
		deviceMAC: "aabbccddeeff",
      metered: true,
      remainingBytes: 500,
      status: "ACTIVE",
      paymentStatus: "PAID",
      startsAt: "2026-08-23T12:00:00Z",
      expiresAt: "2026-08-30T12:00:00Z"
    }],
    deviceReplacementEnabled: false,
    payments: [{
      reference: "pay-0123456789abcdef0123456789abcdef",
      amountMinor: 250000,
      currency: "NGN",
      status: "SUCCESS",
      createdAt: "2026-08-23T12:00:00Z"
    }]
  });
});

test("rejects malformed account response rows before the portal renders them", () => {
  assert.deepEqual(account.displayModel({
    data: {
      subscriptions: [{ plan_name: "", status: "ACTIVE" }],
      payments: [{ reference: "", amount_minor: "250000", currency: "NGN", status: "SUCCESS" }]
    }
  }), { subscriptions: [], payments: [], deviceReplacementEnabled: false });
});

test("offers replacement only for a different MAC on an active unexpired plan with connection context", () => {
  const subscription = { id: "11111111-1111-4111-8111-111111111111", status: "ACTIVE", deviceMAC: "aabbccddeeff", expiresAt: "2026-09-30T00:00:00Z" };
  const now = Date.parse("2026-09-26T00:00:00Z");
  assert.equal(account.canMoveSubscription(subscription, { client_mac: "11:22:33:44:55:66" }, true, now), true);
  assert.equal(account.canMoveSubscription(subscription, { client_mac: "AA:BB:CC:DD:EE:FF" }, true, now), false);
  assert.equal(account.canMoveSubscription(subscription, null, true, now), false);
  assert.equal(account.canMoveSubscription(subscription, { client_mac: "11:22:33:44:55:66" }, false, now), false);
  assert.equal(account.canMoveSubscription(subscription, { client_mac: "11:22:33:44:55:66" }, true, Date.parse("2026-10-01T00:00:00Z")), false);
});
