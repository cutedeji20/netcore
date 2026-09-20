"use strict";
const assert = require("node:assert/strict");
const test = require("node:test");
const pricing = require("./portal-pricing.js");
test("shows a server-supplied N500 plan plus N15 bank charge as N515", () => {
  assert.deepEqual(pricing.breakdown({ price_minor: 50000, bank_charge_minor: 1500, total_minor: 51500, currency: "NGN" }), { priceMinor: 50000, bankChargeMinor: 1500, totalMinor: 51500, currency: "NGN" });
});
test("rejects inconsistent totals", () => {
  assert.equal(pricing.breakdown({ price_minor: 50000, bank_charge_minor: 1500, total_minor: 50000 }), null);
});
