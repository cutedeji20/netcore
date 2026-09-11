"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const integrationDisplay = require("./integration-display.js");

test("shows only safe Resend and Squad metadata", () => {
  const cards = integrationDisplay.toCards([
    { provider: "resend", status: "ACTIVE", sender_email: "NetCore <access@example.test>", updated_at: "2026-08-24T12:00:00Z", credential_ciphertext: "must-not-render" },
    { provider: "squad", status: "ACTIVE", squad_mode: "TEST", updated_at: "2026-08-24T12:00:00Z", kek_key_id: "must-not-render" }
  ]);

  assert.deepEqual(cards, [
    { provider: "resend", name: "Resend", status: "Active", detail: "NetCore <access@example.test>", action: "Update" },
    { provider: "squad", name: "Squad", status: "Active", detail: "Test mode", action: "Update" }
  ]);
});

test("shows disconnected providers as safe setup actions", () => {
  assert.deepEqual(integrationDisplay.toCards([]), [
    { provider: "resend", name: "Resend", status: "Disconnected", detail: "Email verification and receipts", action: "Connect" },
    { provider: "squad", name: "Squad", status: "Disconnected", detail: "Checkout and payment verification", action: "Connect" }
  ]);
});
