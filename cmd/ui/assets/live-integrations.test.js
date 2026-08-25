"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

class Element {
  constructor(name) {
    this.name = name;
    this.children = [];
    this.parentNode = null;
    this.id = "";
    this.className = "";
    this._textContent = "";
    this.attributes = {};
    this.listeners = {};
    this.value = "";
  }

  get textContent() { return this._textContent + this.children.map((child) => child.textContent).join(""); }

  set textContent(value) { this._textContent = String(value); this.children = []; }

  append(...nodes) {
    nodes.forEach((node) => this.appendChild(node));
  }

  appendChild(node) {
    node.parentNode = this;
    this.children.push(node);
    return node;
  }

  insertBefore(node, before) {
    if (before && before.parentNode !== this) throw new Error("NotFoundError: reference node is not a child of this element");
    node.parentNode = this;
    const index = before ? this.children.indexOf(before) : -1;
    if (index === -1) this.children.push(node);
    else this.children.splice(index, 0, node);
    return node;
  }

  remove() {
    if (!this.parentNode) return;
    const index = this.parentNode.children.indexOf(this);
    if (index !== -1) this.parentNode.children.splice(index, 1);
    this.parentNode = null;
  }

  addEventListener() {}

  addEventListener(name, listener) {
    (this.listeners[name] ||= []).push(listener);
  }

  dispatch(name, event = { preventDefault() {} }) {
    (this.listeners[name] || []).forEach((listener) => listener(event));
  }

  click() { this.dispatch("click"); }

  focus() {}

  reset() {}

  setAttribute(name, value) { this.attributes[name] = String(value); }

  querySelector(selector) {
    const matches = (node) => (selector.startsWith("#")
      ? node.id === selector.slice(1)
      : selector.startsWith(".") ? node.className.split(/\s+/).includes(selector.slice(1))
        : node.name === selector);
    const visit = (node) => {
      for (const child of node.children) {
        if (matches(child)) return child;
        const found = visit(child);
        if (found) return found;
      }
      return null;
    };
    return visit(this);
  }

  querySelectorAll(selector) {
    const found = [];
    const visit = (node) => {
      node.children.forEach((child) => {
        if ((selector.startsWith("#") && child.id === selector.slice(1)) || (selector.startsWith(".") && child.className.split(/\s+/).includes(selector.slice(1))) || (!selector.startsWith("#") && !selector.startsWith(".") && child.name === selector)) found.push(child);
        visit(child);
      });
    };
    visit(this);
    return found;
  }
}

test("renders the Settings integrations panel when routing reports Settings without a URL fragment", async () => {
  const body = new Element("body");
  const content = new Element("main");
  content.id = "page-content";
  const table = new Element("section");
  table.className = "panel table";
  content.appendChild(table); body.appendChild(content);
  const listeners = {};
  const window = {
    location: { origin: "https://hotspot.example.test", hash: "" },
    NETCORE_API_URL: "https://hotspot.example.test",
    NetCoreIntegrationDisplay: {
      toCards: () => [{ provider: "resend", name: "Resend", status: "Disconnected", detail: "Email verification", action: "Connect" }]
    },
    NetCoreLivePage: {
      current: () => "overview",
      subscribe: (listener) => { listeners["netcore:page-rendered"] = listener; }
    },
    addEventListener: (name, listener) => { listeners[name] = listener; }
  };
  const document = {
    body,
    createElement: (name) => new Element(name),
    querySelector: (selector) => body.querySelector(selector)
  };
  const source = fs.readFileSync(path.join(__dirname, "live-integrations.js"), "utf8");
  vm.runInNewContext(source, {
    window,
    document,
    fetch: async () => ({ ok: true, json: async () => ({ integrations: [] }) })
  });

  listeners["netcore:page-rendered"]("settings");
  await new Promise((resolve) => setImmediate(resolve));

  assert.ok(content.querySelector("#integration-settings"), "Settings must show the provider connection panel");
});

test("renders the Settings integrations panel beside a nested live table", async () => {
  const body = new Element("body");
  const content = new Element("main");
  content.id = "page-content";
  const splitGrid = new Element("section");
  splitGrid.className = "split-grid";
  const table = new Element("section");
  table.className = "panel table";
  splitGrid.appendChild(table);
  content.appendChild(splitGrid);
  body.appendChild(content);
  const listeners = {};
  const window = {
    location: { origin: "https://hotspot.example.test", hash: "" },
    NETCORE_API_URL: "https://hotspot.example.test",
    NetCoreIntegrationDisplay: {
      toCards: () => [{ provider: "resend", name: "Resend", status: "Disconnected", detail: "Email verification", action: "Connect" }]
    },
    NetCoreLivePage: {
      current: () => "overview",
      subscribe: (listener) => { listeners["netcore:page-rendered"] = listener; }
    },
    addEventListener: (name, listener) => { listeners[name] = listener; }
  };
  const document = {
    body,
    createElement: (name) => new Element(name),
    querySelector: (selector) => body.querySelector(selector)
  };
  const source = fs.readFileSync(path.join(__dirname, "live-integrations.js"), "utf8");
  vm.runInNewContext(source, {
    window,
    document,
    fetch: async () => ({ ok: true, json: async () => ({ integrations: [] }) })
  });

  listeners["netcore:page-rendered"]("settings");
  await new Promise((resolve) => setImmediate(resolve));

  const panel = content.querySelector("#integration-settings");
  assert.ok(panel, "Settings must render the provider connection panel when its records table is nested");
  assert.equal(panel.parentNode, content, "integration panel must be a direct Settings child");
});

test("refetches integration cards after a successful save", async () => {
  const body = new Element("body");
  const content = new Element("main"); content.id = "page-content"; body.appendChild(content);
  const table = new Element("section"); table.className = "panel table"; content.appendChild(table);
  const listeners = {};
  const window = {
    location: { origin: "https://hotspot.example.test", hash: "" },
    NETCORE_API_URL: "https://hotspot.example.test",
    NetCoreIntegrationDisplay: {
      toCards: (integrations) => integrations.map((item) => ({ provider: item.provider, name: item.provider, status: item.status, detail: item.status, action: "Connect" }))
    },
    NetCoreLivePage: { current: () => "overview", subscribe: (listener) => { listeners.rendered = listener; } }
  };
  const responses = [
    { ok: true, json: async () => ({ integrations: [{ provider: "resend", status: "Disconnected" }] }) },
    { ok: true },
    { ok: true, json: async () => ({ integrations: [{ provider: "resend", status: "Active" }] }) }
  ];
  const requests = [];
  const document = { body, createElement: (name) => new Element(name), querySelector: (selector) => body.querySelector(selector) };
  const source = fs.readFileSync(path.join(__dirname, "live-integrations.js"), "utf8");
  vm.runInNewContext(source, { window, document, fetch: async (url, options) => { requests.push({ url, options }); return responses.shift(); }, Promise });

  listeners.rendered("settings");
  await new Promise((resolve) => setImmediate(resolve));
  content.querySelectorAll("button").find((button) => button.textContent === "Connect").click();
  body.querySelector("form").dispatch("submit");
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(requests.length, 3, "saving must refetch integration cards instead of reusing stale payload");
  assert.match(content.textContent, /Active/);
});

test("refetches integration cards after a successful disable", async () => {
  const body = new Element("body");
  const content = new Element("main"); content.id = "page-content"; body.appendChild(content);
  const table = new Element("section"); table.className = "panel table"; content.appendChild(table);
  const listeners = {};
  const window = {
    location: { origin: "https://hotspot.example.test", hash: "" },
    NETCORE_API_URL: "https://hotspot.example.test",
    NetCoreIntegrationDisplay: { toCards: (integrations) => integrations.map((item) => ({ provider: item.provider, name: item.provider, status: item.status, detail: item.status, action: "Configure" })) },
    NetCoreLivePage: { current: () => "overview", subscribe: (listener) => { listeners.rendered = listener; } }
  };
  const responses = [
    { ok: true, json: async () => ({ integrations: [{ provider: "resend", status: "Active" }] }) },
    { ok: true },
    { ok: true, json: async () => ({ integrations: [{ provider: "resend", status: "Disabled" }] }) }
  ];
  const requests = [];
  const document = { body, createElement: (name) => new Element(name), querySelector: (selector) => body.querySelector(selector) };
  const source = fs.readFileSync(path.join(__dirname, "live-integrations.js"), "utf8");
  vm.runInNewContext(source, { window, document, fetch: async (url, options) => { requests.push({ url, options }); return responses.shift(); }, Promise });

  listeners.rendered("settings");
  await new Promise((resolve) => setImmediate(resolve));
  content.querySelectorAll("button").find((button) => button.textContent === "Disable").click();
  body.querySelector("form").dispatch("submit");
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(requests.length, 3, "disabling must refetch integration cards instead of reusing stale payload");
  assert.equal(requests[1].options.method, "POST");
  assert.match(content.textContent, /Disabled/);
});
