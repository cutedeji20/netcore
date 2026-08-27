"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

test("access-operation adapters use their named shared configuration", () => {
  for (const page of ["subscriptions", "sessions", "vouchers"]) {
    const source = fs.readFileSync(path.join(__dirname, "live-" + page + ".js"), "utf8");
    assert.match(source, new RegExp('NetCoreLiveListConfig\\.get\\("' + page + '"\\)'));
    assert.match(source, /NetCoreLiveListControls\.requestURL/);
  }
});

test("shared access configuration creates only supported query strings", () => {
  const config = require("./live-list-config.js");
  const controls = require("./live-list-controls.js");
  const subscriptions = controls.createState(config.get("subscriptions").filters, "SUSPENDED", "status");
  subscriptions.query = "Amina";
  subscriptions.cursor = "opaque";
  assert.equal(controls.requestURL("https://hotspot.example", config.get("subscriptions").endpoint, subscriptions, 25), "https://hotspot.example/api/v1/subscriptions?limit=25&q=Amina&status=SUSPENDED&cursor=opaque");
  const vouchers = controls.createState(config.get("vouchers").filters, "", "");
  vouchers.query = "August";
  vouchers.cursor = "opaque";
  assert.equal(controls.requestURL("https://hotspot.example", config.get("vouchers").endpoint, vouchers, 25), "https://hotspot.example/api/v1/vouchers/batches?limit=25&q=August&cursor=opaque");
});
