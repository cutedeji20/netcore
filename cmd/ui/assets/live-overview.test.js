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

test("overview exposes a retryable, safe failure state instead of silently retaining empty data", () => {
  assert.match(source, /function setOverviewStatus\(message, isError, retry\)/);
  assert.match(source, /Dashboard data is temporarily unavailable/);
  assert.match(source, /data-overview-retry/);
  assert.doesNotMatch(source, /retain safe empty state/);
});
