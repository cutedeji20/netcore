"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const source = fs.readFileSync(path.join(__dirname, "live-plans.js"), "utf8");

test("plan lifecycle uses dedicated retire, restore, and administrator-delete actions", () => {
  assert.match(source, /permissions\.indexOf\("plan\.delete"\)/);
  assert.match(source, /openPublicationDialog\(plan, "RETIRED"\)/);
  assert.match(source, /openPublicationDialog\(plan, "ACTIVE"\)/);
  assert.match(source, /retiring \? "\/retire" : "\/restore"/);
  assert.match(source, /"POST", submit, feedback/);
  assert.match(source, /runPlanLifecycle\(plan, "", "DELETE"/);
});

test("permanent plan deletion requires an exact plan-name confirmation", () => {
  assert.match(source, /input\.value !== plan\.name/);
  assert.match(source, /Type the exact plan name to confirm/);
  assert.match(source, /Existing active subscriptions keep access until their normal expiry/);
});

test("ordinary plan editing cannot silently change a plan publication state", () => {
  assert.doesNotMatch(source, /createInput\("Availability", "status"/);
  assert.match(source, /status: current && current\.status \? current\.status : "ACTIVE"/);
});
