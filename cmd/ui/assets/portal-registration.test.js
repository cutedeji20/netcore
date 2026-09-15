"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const registration = require("./portal-registration.js");

test("requires a country-code phone number before accepting a registration", () => {
  assert.equal(
    registration.validate("customer@example.com", "Pass1234", "Pass1234", "08012345678"),
    "Enter a valid phone number with country code."
  );
});

test("requires a 4 to 12 character alpha-numeric customer password", () => {
  assert.equal(registration.validate("customer@example.com", "abc", "abc", "+2348012345678"), "Your password must be 4 to 12 characters.");
  assert.equal(registration.validate("customer@example.com", "Pass1234!", "Pass1234!", "+2348012345678"), "Use letters and numbers only for your password.");
  assert.equal(registration.validate("customer@example.com", "Pass1234", "Pass1234", "+2348012345678"), "");
});
