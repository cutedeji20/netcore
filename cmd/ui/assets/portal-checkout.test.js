"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const checkout = require("./portal-checkout.js");

function memoryStorage() {
  const entries = new Map();
  return {
    getItem(key) { return entries.has(key) ? entries.get(key) : null; },
    setItem(key, value) { entries.set(key, String(value)); },
    removeItem(key) { entries.delete(key); }
  };
}

test("remembers a Paystack return with its captive connection context", () => {
  const storage = memoryStorage();
  const reference = "pay-1234567890abcdef1234567890abcdef";
  const connection = {
    client_mac: "00:11:22:33:44:55",
    nas_address: "10.10.0.1",
    hotspot_login_url: "http://10.10.0.1/login"
  };

  assert.equal(checkout.rememberPaymentReturn(storage, reference, connection), true);
  assert.deepEqual(checkout.readPaymentReturn(storage), { reference, connection });
  checkout.clearPaymentReturn(storage);
  assert.equal(checkout.readPaymentReturn(storage), null);
});

test("rejects malformed payment return state instead of restoring it", () => {
  const storage = memoryStorage();
  storage.setItem(checkout.storageKey, JSON.stringify({
    reference: "payment-from-another-system",
    connection: { client_mac: "x", nas_address: "y", hotspot_login_url: "javascript:alert(1)" }
  }));

  assert.equal(checkout.readPaymentReturn(storage), null);
  assert.equal(storage.getItem(checkout.storageKey), null);
});

test("builds checkout payloads only when both plan and selected device are UUIDs", () => {
  const planID = "33333333-3333-4333-8333-333333333333";
  const deviceID = "44444444-4444-4444-8444-444444444444";

  assert.equal(checkout.paymentRequest(planID, ""), null);
  assert.equal(checkout.paymentRequest("not-a-plan", deviceID), null);
  assert.deepEqual(checkout.paymentRequest(planID, deviceID), { plan_id: planID, device_id: deviceID });
});

test("excludes removed devices from checkout choices", () => {
  const activeID = "44444444-4444-4444-8444-444444444444";
  const removedID = "55555555-5555-4555-8555-555555555555";

  assert.deepEqual(checkout.activeDevices([
    { id: activeID, status: "ACTIVE", normalized_mac: "aabbccddeeff" },
    { id: removedID, status: "REMOVED", normalized_mac: "001122334455" },
    { id: "not-a-device", status: "ACTIVE" }
  ]), [{ id: activeID, status: "ACTIVE", normalized_mac: "aabbccddeeff" }]);
});
