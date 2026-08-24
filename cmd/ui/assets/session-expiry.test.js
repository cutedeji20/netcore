"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const sessionExpiry = require("./session-expiry.js");

test("locks the dashboard at the server-provided session expiry", () => {
  let expired = 0;
  let scheduledDelay = null;
  let scheduled = null;
  const cancel = sessionExpiry.arm("2026-08-24T18:30:00Z", {
    now: () => Date.parse("2026-08-24T18:00:00Z"),
    setTimeout: (callback, delay) => {
      scheduled = callback;
      scheduledDelay = delay;
      return 7;
    },
    clearTimeout: () => assert.fail("the new timer must not be cancelled"),
    onExpired: () => { expired++; }
  });

  assert.equal(scheduledDelay, 30 * 60 * 1000);
  scheduled();
  assert.equal(expired, 1);
  assert.equal(typeof cancel, "function");
});

test("locks the dashboard when an expiry cannot be verified", () => {
  let expired = 0;

  sessionExpiry.arm("not-a-date", {
    onExpired: () => { expired++; }
  });

  assert.equal(expired, 1);
});
