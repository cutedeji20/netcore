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
