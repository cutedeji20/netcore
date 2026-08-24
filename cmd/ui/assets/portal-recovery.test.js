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
  assert.equal(recovery.canConfirm("a secure password", "a secure password"), true);
  assert.equal(recovery.canConfirm("a secure password", "different password"), false);
  assert.equal(recovery.canConfirm("short", "short"), false);
});
