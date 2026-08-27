"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

class Element {
  constructor(name) { this.name = name; this.children = []; this.parentNode = null; this.id = ""; this.className = ""; this.attributes = {}; this.listeners = {}; this._text = ""; }
  get textContent() { return this._text + this.children.map((child) => child.textContent).join(""); }
  set textContent(value) { this._text = String(value); this.children = []; }
  append(...nodes) { nodes.forEach((node) => this.appendChild(node)); }
  appendChild(node) { node.parentNode = this; this.children.push(node); return node; }
  replaceChildren(...nodes) { this.children = []; this._text = ""; this.append(...nodes); }
  insertBefore(node, before) { node.parentNode = this; const index = before ? this.children.indexOf(before) : -1; this.children.splice(index < 0 ? this.children.length : index, 0, node); return node; }
  remove() { if (!this.parentNode) return; const index = this.parentNode.children.indexOf(this); if (index >= 0) this.parentNode.children.splice(index, 1); this.parentNode = null; }
  setAttribute(name, value) { this.attributes[name] = String(value); }
  getAttribute(name) { return this.attributes[name]; }
  addEventListener(name, listener) { (this.listeners[name] ||= []).push(listener); }
  click() { (this.listeners.click || []).forEach((listener) => listener({ preventDefault() {} })); }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
  querySelectorAll(selector) {
    const parts = selector.trim().split(/\s+/);
    const matches = (node, part) => {
      const attribute = /^\[([^=]+)="?([^\]"]+)"?\]$/.exec(part);
      return attribute ? node.getAttribute(attribute[1]) === attribute[2] : part.startsWith("#") ? node.id === part.slice(1) : part.startsWith(".") ? node.className.split(/\s+/).includes(part.slice(1)) : node.name === part;
    };
    const descendants = (node) => node.children.flatMap((child) => [child, ...descendants(child)]);
    let candidates = [this];
    for (const part of parts) candidates = candidates.flatMap(descendants).filter((node) => matches(node, part));
    return candidates;
  }
}

const adapterSpecs = [
  ["live-customers.js", "customers", () => ({ data: [{}] }), () => ({ data: [] })],
  ["live-subscriptions.js", "subscriptions", () => ({ data: [{}] }), () => ({ data: [] })],
  ["live-plans.js", "plans", () => ({ data: [{}] }), () => ({ data: [] })],
  ["live-sessions.js", "sessions", () => ({ data: [{}] }), () => ({ data: [] })],
  ["live-billing.js", "billing", () => ({ data: [{}] }), () => ({ data: [] })],
  ["live-network.js", "network", () => ({ data: [{}] }), () => ({ data: [] })],
  ["live-vouchers.js", "vouchers", () => ({ data: [{}] }), () => ({ data: [] })],
  ["live-team.js", "team", () => ({ data: [{}] }), () => ({ data: [] })],
  ["live-security.js", "security", () => ({ data: [{}] }), () => ({ data: [] })],
  ["live-automations.js", "automations", () => ({ data: [{}] }), () => ({ data: [] })],
  ["live-workspace.js", "settings", () => ({ name: "Lagos", status: "ACTIVE" }), () => ({}), "table"],
  ["live-payment-readiness.js", "billing", () => ({ provider: "paystack", checkout_status: "ready" }), () => ({ provider: "paystack", checkout_status: "empty" }), "payment"],
  ["live-integrations.js", "settings", () => ({ integrations: [{}] }), () => ({ integrations: [] }), "integrations"]
];

function response(payload) { return { ok: true, json: async () => payload }; }
async function settled() { await new Promise((resolve) => setImmediate(resolve)); await new Promise((resolve) => setImmediate(resolve)); }

function environment(file, responses, initialHash) {
  const body = new Element("body"); const content = new Element("main"); content.id = "page-content";
  const heading = new Element("div"); heading.className = "page-heading"; const split = new Element("section"); split.className = "split-grid";
  const panel = new Element("section"); panel.className = "panel table"; const table = new Element("table"); table.className = "data-table";
  const head = new Element("thead"); const row = new Element("tr"); head.appendChild(row); for (let index = 0; index < 7; index++) row.appendChild(new Element("th"));
  const tableBody = new Element("tbody"); table.append(head, tableBody); panel.appendChild(table); split.appendChild(panel); content.append(heading, split); body.appendChild(content);
  ["subscriptions", "sessions", "vouchers", "network", "billing", "security", "automations"].forEach((page) => {
    const controls = new Element("div"); controls.setAttribute("data-live-list-controls", page);
    const pagination = new Element("div"); pagination.setAttribute("data-live-list-pagination", page);
    heading.append(controls, pagination);
  });
  const listeners = {};
  const window = {
    location: { origin: "https://hotspot.example.test", hash: initialHash || "#overview" }, NETCORE_API_URL: "https://hotspot.example.test", NETCORE_PRINCIPAL: { permissions: [] },
    NetCorePaymentReadiness: { toDisplay: (payload) => ({ tone: "ready", title: payload.checkout_status, rows: payload.checkout_status === "empty" ? [] : [["Provider", payload.provider]] }) },
    NetCoreIntegrationDisplay: { toCards: (items) => items.map((item) => ({ provider: item.provider || "resend", name: "Resend", status: "Active", detail: "Configured", action: "Configure" })) },
    addEventListener(name, listener) { (listeners[name] ||= []).push(listener); }
  };
  const document = { body, createElement: (name) => new Element(name), createTextNode: (text) => { const node = new Element("#text"); node.textContent = text; return node; }, querySelector: (selector) => body.querySelector(selector), querySelectorAll: (selector) => body.querySelectorAll(selector) };
  const source = (name) => fs.readFileSync(path.join(__dirname, name), "utf8"); let request = 0; const requests = [];
  const fetch = (url) => { const next = responses[request++]; requests.push(url); return typeof next === "function" ? next(url) : next instanceof Error ? Promise.reject(next) : Promise.resolve(next); };
  vm.runInNewContext(source("live-page.js"), { window, document });
  vm.runInNewContext(source("live-list-config.js"), { window });
  vm.runInNewContext(source("live-list-controls.js"), { window, URL });
  vm.runInNewContext(source(file), { window, document, fetch, Promise, Date, Number, setTimeout, clearTimeout });
  return { content, table, tableBody, window, requests, render(page) { (listeners["netcore:page-rendered"] || []).forEach((listener) => listener({ detail: page })); }, state(kind) { return kind === "payment" ? content.querySelector("#payment-readiness") : kind === "integrations" ? content.querySelector("#integration-settings") : table; } };
}

test("adapter harness exposes shared live-list dependencies before adapters run", () => {
  const loaded = environment("live-subscriptions.js", []);
  assert.equal(typeof loaded.window.NetCoreLiveListConfig.get, "function");
  assert.equal(typeof loaded.window.NetCoreLiveListControls.createState, "function");
});

test("list adapters wait for their first rendered-page event before loading a hash-matched page", async () => {
  for (const [file, page] of [
    ["live-subscriptions.js", "subscriptions"], ["live-sessions.js", "sessions"], ["live-vouchers.js", "vouchers"],
    ["live-network.js", "network"], ["live-billing.js", "billing"], ["live-security.js", "security"], ["live-automations.js", "automations"]
  ]) {
    const loaded = environment(file, [response({ data: [] })], "#" + page);
    assert.equal(loaded.requests.length, 0, `${file} must stay idle before the authenticated rendered-page event`);
    loaded.render(page);
    assert.equal(loaded.requests.length, 1, `${file} must load once after its first matching rendered-page event`);
    await settled();
  }
});

test("every named adapter follows rendered routes and shows loading, records, and empty results", async () => {
  for (const [file, page, recordPayload, emptyPayload, kind] of adapterSpecs) {
    const loaded = environment(file, [response(recordPayload())]); loaded.render(page);
    assert.equal(loaded.window.NetCoreLivePage.current(), page, `${file} must follow the rendered route instead of the stale hash`);
    assert.equal(loaded.state(kind).getAttribute("data-live-state"), "loading", `${file} must visibly load`);
    await settled();
    assert.equal(loaded.state(kind).getAttribute("data-live-state"), "records", `${file} must visibly show verified records`);
    const empty = environment(file, [response(emptyPayload())]); empty.render(page); await settled();
    assert.equal(empty.state(kind).getAttribute("data-live-state"), "empty", `${file} must visibly show an intentional empty result`);
  }
});

test("every named adapter exposes a retryable error and preserves verified records through its actual refresh path", async () => {
  for (const [file, page, recordPayload, , kind] of adapterSpecs) {
    const failed = environment(file, [new Error("offline"), response(recordPayload())]); failed.render(page); await settled();
    const errorState = failed.state(kind); assert.equal(errorState.getAttribute("data-live-state"), "error", `${file} must visibly show a failed request`);
    const retry = ((kind === "payment" || kind === "integrations") ? errorState : errorState.parentNode).querySelector("button"); assert.ok(retry, `${file} must offer a retry action`); retry.click(); await settled();
    assert.equal(failed.state(kind).getAttribute("data-live-state"), "records", `${file} retry must load verified data`);
    const preserved = environment(file, [response(recordPayload()), new Error("offline")]); preserved.render(page); await settled();
    const rowsBeforeFailure = preserved.tableBody.children.length;
    preserved.render(page); await settled();
    if (kind === "payment" || kind === "integrations") {
      assert.equal(preserved.state(kind).getAttribute("data-live-state"), "records", `${file} must retain its verified readiness state`);
      assert.match(preserved.content.textContent, /could not be (refreshed|loaded)/i, `${file} must visibly report a refresh failure`);
      assert.ok(preserved.state(kind).querySelector("button"), `${file} must offer a retry after a failed refresh`);
      if (kind === "payment") assert.ok(preserved.state(kind).querySelector("ul"), `${file} must retain verified readiness details`);
      if (kind === "integrations") assert.ok(preserved.state(kind).querySelector(".integration-card"), `${file} must retain verified integration cards`);
    } else {
      assert.equal(preserved.state(kind).getAttribute("data-live-state"), "error", `${file} must expose a refresh failure`);
    }
    assert.equal(preserved.tableBody.children.length, rowsBeforeFailure, `${file} must retain verified rows when its refresh fails`);
  }
});

test("list adapters do not paginate or render stale responses while new criteria debounce", async () => {
  for (const [file, page] of [
    ["live-subscriptions.js", "subscriptions"], ["live-sessions.js", "sessions"], ["live-vouchers.js", "vouchers"],
    ["live-network.js", "network"], ["live-billing.js", "billing"], ["live-security.js", "security"], ["live-automations.js", "automations"]
  ]) {
    let resolveOld;
    let resolveCurrent;
    const loaded = environment(file, [
      response({ data: [{}], meta: { has_more: true, next_cursor: "next-page" } }),
      () => new Promise((resolve) => { resolveOld = resolve; }),
      () => new Promise((resolve) => { resolveCurrent = resolve; })
    ]);
    loaded.render(page); await settled();
    const input = loaded.content.querySelector('[data-live-list-controls="' + page + '"] input');
    const next = loaded.content.querySelectorAll('[data-live-list-pagination="' + page + '"] button')[1];
    input.value = "current query";
    input.listeners.input[0]();
    next.click();
    assert.equal(loaded.requests.length, 1, `${file} must not paginate while criteria are pending`);
    loaded.render(page);
    assert.equal(loaded.requests.length, 2, `${file} must keep the refresh request separate from pagination`);
    resolveOld(response({ data: [], meta: { has_more: false } }));
    await settled();
    assert.equal(loaded.state().getAttribute("data-live-state"), "records", `${file} must not render the stale response`);
    assert.equal(loaded.requests.length, 2, `${file} must keep the criteria network refresh debounced`);
    await new Promise((resolve) => setTimeout(resolve, 275));
    assert.equal(loaded.requests.length, 3, `${file} must request the debounced criteria after discarding stale data`);
    assert.match(loaded.requests[2], /q=current\+query/, `${file} must request the typed criteria`);
    resolveCurrent(response({ data: [{}], meta: { has_more: false } }));
    await settled();
  }
});
