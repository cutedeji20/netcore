"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const source = fs.readFileSync(path.join(__dirname, "live-network.js"), "utf8");

test("network operations expose permission-gated router onboarding controls", () => {
  assert.match(source, /permissions\.indexOf\("network\.write"\)/);
  assert.match(source, /Add router/);
  assert.match(source, /Configure AAA/);
  assert.match(source, /Download setup/);
  assert.match(source, /Record private test/);
  assert.match(source, /Activate AAA/);
  assert.match(source, /Disable AAA/);
});

test("router setup export downloads a short-lived file without rendering or storing the secret", () => {
  assert.match(source, /response\.blob\(\)/);
  assert.match(source, /URL\.createObjectURL\(blob\)/);
  assert.match(source, /URL\.revokeObjectURL\(downloadURL\)/);
  assert.doesNotMatch(source, /localStorage/);
  assert.doesNotMatch(source, /sessionStorage/);
  assert.doesNotMatch(source, /innerHTML/);
});

test("AAA activation requires an explicit private Access-Request and accounting confirmation", () => {
  assert.match(source, /private_test_confirmed/);
  assert.match(source, /Access-Request and accounting test/);
  assert.match(source, /\/aaa\/verify/);
});
