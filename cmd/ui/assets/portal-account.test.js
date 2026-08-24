"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const account = require("./portal-account.js");

test("builds a customer-only account display model from the portal response", () => {
  const value = account.displayModel({
    data: {
      customer_id: "must-not-reach-the-page",
      subscriptions: [{
        plan_name: "Weekly access",
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
      planName: "Weekly access",
      status: "ACTIVE",
      paymentStatus: "PAID",
      startsAt: "2026-08-23T12:00:00Z",
      expiresAt: "2026-08-30T12:00:00Z"
    }],
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
  }), { subscriptions: [], payments: [] });
});
