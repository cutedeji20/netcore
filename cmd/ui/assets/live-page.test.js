"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

function createLivePageState(hash) {
  const listeners = {};
  const window = {
    location: { hash },
    addEventListener(name, listener) {
      (listeners[name] ||= []).push(listener);
    }
  };
  const source = fs.readFileSync(path.join(__dirname, "live-page.js"), "utf8");
  vm.runInNewContext(source, { window, document: {} });
  return {
    current: () => window.NetCoreLivePage.current(),
    onRendered(page) {
      (listeners["netcore:page-rendered"] || []).forEach((listener) => listener({ detail: page }));
    }
  };
}

test("active page follows render event when the hash is stale", () => {
  const state = createLivePageState("#overview");
  state.onRendered("customers");
  assert.equal(state.current(), "customers");
});

test("page subscribers receive rendered routes even when the hash is stale", () => {
  const state = createLivePageState("#overview");
  const received = [];
  const listeners = {};
  const window = {
    location: { hash: "#overview" },
    addEventListener(name, listener) {
      (listeners[name] ||= []).push(listener);
    }
  };
  const source = fs.readFileSync(path.join(__dirname, "live-page.js"), "utf8");
  vm.runInNewContext(source, { window, document: {} });
  window.NetCoreLivePage.subscribe((page) => received.push(page));
  listeners["netcore:page-rendered"].forEach((listener) => listener({ detail: "billing" }));
  assert.deepEqual(received, ["billing"]);
});

test("read-only operational pages do not render unavailable primary actions", () => {
  const source = fs.readFileSync(path.join(__dirname, "app.js"), "utf8");
  for (const action of ["New subscription", "Find session", "Create voucher batch", "Add network device", "Create invoice", "Review activity", "Create automation", "Save changes"]) {
    assert.equal(source.includes('data-toast="' + action + ' is ready'), false, action + " must not be a fake CTA");
  }
});

test("list mounts follow rendered page changes instead of a stale hash", () => {
  const source = fs.readFileSync(path.join(__dirname, "live-page.js"), "utf8");
  assert.match(source, /function listMount\(page\)/);
  assert.match(source, /function renderListControls\(page, options\)/);
  const state = createLivePageState("#overview");
  state.onRendered("settings");
  state.onRendered("sessions");
  assert.equal(state.current(), "sessions");
});

test("settings keeps its integration script but loses the generic Save CTA", () => {
  const source = fs.readFileSync(path.join(__dirname, "app.js"), "utf8");
  assert.match(source, /live-integrations\.js/);
  assert.equal(source.includes('data-toast="Save changes is ready'), false);
});

test("live list configuration loads before controls and page adapters", () => {
  const source = fs.readFileSync(path.join(__dirname, "app.js"), "utf8");
  const config = source.indexOf('"/live-list-config.js"');
  const controls = source.indexOf('"/live-list-controls.js"');
  const adapters = source.indexOf('"/live-subscriptions.js"');
  assert.ok(config >= 0, "live list configuration must load");
  assert.ok(config < controls, "configuration must load before controls");
  assert.ok(controls < adapters, "controls must load before page adapters");
});
