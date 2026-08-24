"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const readiness = require("./payment-readiness.js");

test("presents a ready Paystack configuration with only public endpoints", () => {
  const view = readiness.toDisplay({
    provider: "paystack",
    checkout_status: "READY",
    callback_url: "https://hotspot.example.test/portal.html",
    webhook_url: "https://hotspot.example.test/webhooks/paystack"
  });

  assert.deepEqual(view, {
    tone: "ready",
    title: "Paystack is ready for checkout",
    rows: [
      ["Provider", "Paystack"],
      ["Customer return", "https://hotspot.example.test/portal.html"],
      ["Webhook receiver", "https://hotspot.example.test/webhooks/paystack"]
    ]
  });
});

test("does not present endpoints when payments are disabled", () => {
  const view = readiness.toDisplay({
    provider: "disabled",
    checkout_status: "DISABLED",
    callback_url: "https://hotspot.example.test/portal.html",
    webhook_url: "https://hotspot.example.test/webhooks/paystack"
  });

  assert.deepEqual(view, {
    tone: "disabled",
    title: "Payments are disabled",
    rows: [["Provider", "Not configured"]]
  });
});
