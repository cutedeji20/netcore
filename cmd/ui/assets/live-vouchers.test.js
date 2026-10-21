"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const source = fs.readFileSync(path.join(__dirname, "live-vouchers.js"), "utf8");
const css = fs.readFileSync(path.join(__dirname, "app.css"), "utf8");

test("voucher issue action belongs to the page heading and opens a dialog", () => {
  assert.match(source, /\.page-heading/);
  assert.match(source, /openIssueDialog/);
  assert.match(source, /aria-modal/);
  assert.doesNotMatch(source, /host\.prepend\(panel\)/);
});

test("code management uses its own dialog instead of expanding a table cell", () => {
  assert.match(source, /openManageDialog/);
  assert.doesNotMatch(source, /cell\.append\(form\)/);
  assert.match(css, /\.voucher-dialog/);
  assert.match(css, /\.voucher-form/);
});

test("revealing a batch uses the authenticated session without another challenge", () => {
  const reveal = source.slice(source.indexOf("function openManageDialog"), source.indexOf("livePage.subscribe"));
  assert.match(reveal, /body:"\{\}"/);
  assert.doesNotMatch(reveal, /voucherField\("Current password"/);
  assert.doesNotMatch(reveal, /voucherField\("Authenticator code"/);
});
