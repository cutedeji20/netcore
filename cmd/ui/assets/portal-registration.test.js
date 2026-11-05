"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const registration = require("./portal-registration.js");

test("accepts Nigerian mobile numbers with or without the country code", () => {
  assert.equal(
    registration.validate("customer@example.com", "Pass1234", "Pass1234", "08012345678"),
    ""
  );
  assert.equal(registration.validate("customer@example.com", "Pass1234", "Pass1234", "+2348012345678"), "");
  assert.equal(registration.validate("customer@example.com", "Pass1234", "Pass1234", "2348012345678"), "");
  assert.equal(registration.validate("customer@example.com", "Pass1234", "Pass1234", "12345"), "Enter a valid Nigerian mobile phone number.");
});

test("requires a valid email address before account creation", () => {
  assert.equal(registration.validate("not an email", "Pass1234", "Pass1234", "+2348012345678"), "Enter a valid email address.");
  assert.equal(registration.validate("customer@example.com", "Pass1234", "Pass1234", "+2348012345678"), "");
});

test("requires a 4 to 12 character alpha-numeric customer password", () => {
  assert.equal(registration.validate("customer@example.com", "abc", "abc", "+2348012345678"), "Your password must be 4 to 12 characters.");
  assert.equal(registration.validate("customer@example.com", "Pass1234!", "Pass1234!", "+2348012345678"), "Use letters and numbers only for your password.");
  assert.equal(registration.validate("customer@example.com", "Pass1234", "Pass1234", "+2348012345678"), "");
});
