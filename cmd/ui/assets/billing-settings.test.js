"use strict";
const assert = require("node:assert/strict");
const test = require("node:test");
const settings = require("./billing-settings.js");
test("only billing.write can edit and decimal NGN input stays exact", () => {
  assert.equal(settings.canEdit({ permissions: ["billing.write"] }), true);
  assert.equal(settings.canEdit({ permissions: ["billing.read"] }), false);
  assert.equal(settings.exactNGN("15.00"), true);
  assert.equal(settings.exactNGN("15.001"), false);
});
