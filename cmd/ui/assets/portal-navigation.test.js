"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const navigation = require("./portal-navigation.js");

test("opens the customer account after a sign-in outside the captive network", () => {
  assert.equal(navigation.destinationAfterSignIn({
    hasConnection: false,
    hasReturningPayment: false,
    hasSelectedPlan: false,
    accountRequested: false
  }), "account");
});

test("preserves payment, plan, account, and captive handoff priorities after sign-in", () => {
  assert.equal(navigation.destinationAfterSignIn({ hasConnection: true, hasReturningPayment: true }), "payment");
  assert.equal(navigation.destinationAfterSignIn({ hasConnection: true, hasSelectedPlan: true }), "checkout");
  assert.equal(navigation.destinationAfterSignIn({ hasConnection: true, accountRequested: true }), "account");
  assert.equal(navigation.destinationAfterSignIn({ hasConnection: true }), "handoff");
});
