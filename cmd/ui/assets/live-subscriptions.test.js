const assert = require("node:assert/strict");
const test = require("node:test");
const { subscriptionDeviceLabel, canTransferSubscription } = require("./live-subscriptions.js");

test("subscription device shows label and readable MAC", () => {
  assert.equal(subscriptionDeviceLabel({ label: "MAAL", normalized_mac: "46388ddbb0f9" }), "MAAL · 46:38:8D:DB:B0:F9");
  assert.equal(subscriptionDeviceLabel({ normalized_mac: "d2bd24d25372" }), "Device · D2:BD:24:D2:53:72");
  assert.equal(subscriptionDeviceLabel(null), "Not assigned");
});

test("admin transfer appears only for active unexpired bound plans when enabled", () => {
  const value = { id: "s", status: "ACTIVE", expires_at: "2099-01-01T00:00:00Z", device: { id: "d" } };
  assert.equal(canTransferSubscription(value, true, true, Date.parse("2026-09-25T00:00:00Z")), true);
  assert.equal(canTransferSubscription(value, false, true, Date.parse("2026-09-25T00:00:00Z")), false);
  assert.equal(canTransferSubscription(value, true, false, Date.parse("2026-09-25T00:00:00Z")), false);
  assert.equal(canTransferSubscription({ ...value, device: null }, true, true, Date.parse("2026-09-25T00:00:00Z")), false);
});
