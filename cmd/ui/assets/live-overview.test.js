"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const source = fs.readFileSync(path.join(__dirname, "live-overview.js"), "utf8");

test("overview renders verified WAN periods and daily history without representative values", () => {
  assert.match(source, /function showWANHistory\(traffic\)/);
  assert.match(source, /traffic\.wan_periods/);
  assert.match(source, /traffic\.wan_daily/);
  assert.match(source, /Daily WAN totals \(last 30 days\)/);
  assert.match(source, /data-wan-traffic-history/);
  assert.doesNotMatch(source, /30GB|20GB/);
});
