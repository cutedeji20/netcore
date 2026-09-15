"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const recovery = require("./portal-recovery.js");

test("keeps only a valid reset e-mail and opaque challenge", () => {
  assert.deepEqual(recovery.createChallenge(" Customer@Example.com ", "challenge-1234567890abcdef"), {
    email: "customer@example.com",
    challengeID: "challenge-1234567890abcdef"
  });
  assert.equal(recovery.createChallenge("customer@example.com", ""), null);
});

test("requires matching customer reset passwords", () => {
  assert.equal(recovery.canConfirm("Pass1234", "Pass1234"), true);
  assert.equal(recovery.canConfirm("Pass1234", "Pass1235"), false);
  assert.equal(recovery.canConfirm("abc", "abc"), false);
  assert.equal(recovery.canConfirm("Pass1234!", "Pass1234!"), false);
});
