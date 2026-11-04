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

test("voucher inventory shows the purchasing customer or issuing staff and supports status filters", () => {
  assert.match(source, /Owner/);
  assert.match(source, /owner_display/);
  assert.match(source, /Purchased by customer/);
  assert.match(source, /filterOptions/);
  assert.match(source, /Search batch, plan, or customer/);
});

test("voucher inventory displays the server generation timestamp in Nigeria time", () => {
  assert.match(source, /"Generated"/);
  assert.match(source, /batch\.created_at/);
  assert.match(source, /timeZone: "Africa\/Lagos"/);
  assert.match(source, /voucher-generated-cell/);
  assert.match(css, /voucher-generated-inline/);
});

test("revealing a batch uses the authenticated session without another challenge", () => {
  const reveal = source.slice(source.indexOf("function openManageDialog"), source.indexOf("livePage.subscribe"));
  assert.match(reveal, /body:"\{\}"/);
  assert.doesNotMatch(reveal, /voucherField\("Current password"/);
  assert.doesNotMatch(reveal, /voucherField\("Authenticator code"/);
});

test("batch management downloads the audited twenty-ticket PDF", () => {
  const manage = source.slice(source.indexOf("function openManageDialog"), source.indexOf("livePage.subscribe"));
  assert.match(manage, /\/print/);
  assert.match(manage, /Print PDF \(20 per page\)/);
  assert.match(manage, /response\.blob\(\)/);
  assert.match(manage, /URL\.revokeObjectURL/);
});
