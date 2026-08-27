"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

test("operations adapters use their named shared configuration", () => {
  for (const page of ["network", "billing", "security", "automations"]) {
    const source = fs.readFileSync(path.join(__dirname, "live-" + page + ".js"), "utf8");
    assert.match(source, new RegExp('NetCoreLiveListConfig\\.get\\("' + page + '"\\)'));
    assert.match(source, /NetCoreLiveListControls\.requestURL/);
  }
});

test("shared operations configuration creates only supported query strings", () => {
  const config = require("./live-list-config.js");
  const controls = require("./live-list-controls.js");
  for (const item of [
    ["network", "Lekki", "OFFLINE", "n", "/api/v1/network/routers?limit=25&q=Lekki&status=OFFLINE&cursor=n"],
    ["billing", "INV", "INVOICE", "b", "/api/v1/billing/transactions?limit=25&q=INV&source=INVOICE&cursor=b"],
    ["automations", "renewal", "READY", "a", "/api/v1/automations?limit=25&q=renewal&status=READY&cursor=a"],
    ["security", "login", "", "s", "/api/v1/security/events?limit=25&q=login&cursor=s"]
  ]) {
    const page = config.get(item[0]);
    const state = controls.createState(page.filters, item[2], page.filterParam);
    state.query = item[1]; state.cursor = item[3];
    assert.equal(controls.requestURL("https://hotspot.example", page.endpoint, state, 25), "https://hotspot.example" + item[4]);
  }
});
