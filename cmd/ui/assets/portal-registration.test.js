"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const registration = require("./portal-registration.js");

test("stops a matching customer password that is shorter than twelve characters", () => {
  assert.equal(
    registration.validate("customer@example.com", "short-pass", "short-pass"),
    "Your password must be at least 12 characters."
  );
});
