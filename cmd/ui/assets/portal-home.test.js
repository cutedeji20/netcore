"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const html = fs.readFileSync(path.join(__dirname, "portal.html"), "utf8");

test("portal home presents compact access choices and a collapsed voucher form", () => {
  assert.doesNotMatch(html, /You’re connected\. Let’s get you online\./);
  assert.match(html, /data-action="plans">Choose a plan/);
  assert.match(html, /data-action="account">Sign in or manage my plan/);
  assert.match(html, /data-action="buy-vouchers" hidden>Buy vouchers/);
  assert.match(html, /data-action="voucher" aria-expanded="false">Use voucher/);
  assert.match(html, /id="portal-voucher" class="voucher-panel" novalidate hidden/);
  assert.match(html, /placeholder="NCXXXXXXXXXXXX"/);
  assert.match(html, />Connect with voucher/);
});
