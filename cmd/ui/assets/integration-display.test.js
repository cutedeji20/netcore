"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const integrationDisplay = require("./integration-display.js");

test("shows only safe provider metadata and the selected payment gateway", () => {
  const cards = integrationDisplay.toCards([
    { provider: "resend", status: "ACTIVE", sender_email: "NetCore <access@example.test>", updated_at: "2026-08-24T12:00:00Z", credential_ciphertext: "must-not-render" },
    { provider: "squad", status: "ACTIVE", squad_mode: "TEST", payment_default: true, updated_at: "2026-08-24T12:00:00Z", kek_key_id: "must-not-render" }
  ]);

  assert.deepEqual(cards, [
    { provider: "resend", name: "Resend", status: "Active", detail: "NetCore <access@example.test>", action: "Update", paymentDefault: false },
    { provider: "paystack", name: "Paystack", status: "Disconnected", detail: "Checkout and payment verification", action: "Connect", paymentDefault: false },
    { provider: "squad", name: "Squad", status: "Active", detail: "Test mode", action: "Update", paymentDefault: true },
    { provider: "palmpay", name: "PalmPay", status: "Disconnected", detail: "Checkout and payment verification", action: "Connect", paymentDefault: false }
  ]);
});

test("shows disconnected providers as safe setup actions", () => {
  assert.deepEqual(integrationDisplay.toCards([]), [
    { provider: "resend", name: "Resend", status: "Disconnected", detail: "Email verification and receipts", action: "Connect", paymentDefault: false },
    { provider: "paystack", name: "Paystack", status: "Disconnected", detail: "Checkout and payment verification", action: "Connect", paymentDefault: false },
    { provider: "squad", name: "Squad", status: "Disconnected", detail: "Checkout and payment verification", action: "Connect", paymentDefault: false },
    { provider: "palmpay", name: "PalmPay", status: "Disconnected", detail: "Checkout and payment verification", action: "Connect", paymentDefault: false }
  ]);
});
