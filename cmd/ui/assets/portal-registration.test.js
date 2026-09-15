"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const registration = require("./portal-registration.js");

test("requires a country-code phone number before accepting a registration", () => {
  assert.equal(
    registration.validate("customer@example.com", "correct customer password", "correct customer password", "08012345678"),
    "Enter a valid phone number with country code."
  );
});
