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
    this.textContent = "";
  }

  append(...nodes) {
    nodes.forEach((node) => this.appendChild(node));
  }

  appendChild(node) {
    node.parentNode = this;
    this.children.push(node);
    return node;
  }

  insertBefore(node, before) {
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

  setAttribute() {}

  querySelector(selector) {
    const matches = (node) => (selector.startsWith("#")
      ? node.id === selector.slice(1)
      : selector.startsWith(".") && node.className.split(/\s+/).includes(selector.slice(1)));
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
}

test("renders the Settings integrations panel when routing reports Settings without a URL fragment", async () => {
  const content = new Element("main");
  content.id = "page-content";
  const table = new Element("section");
  table.className = "panel table";
  content.appendChild(table);
  const listeners = {};
  const window = {
    location: { origin: "https://hotspot.example.test", hash: "" },
    NETCORE_API_URL: "https://hotspot.example.test",
    NetCoreIntegrationDisplay: {
      toCards: () => [{ provider: "resend", name: "Resend", status: "Disconnected", detail: "Email verification", action: "Connect" }]
    },
    addEventListener: (name, listener) => { listeners[name] = listener; }
  };
  const document = {
    body: new Element("body"),
    createElement: (name) => new Element(name),
    querySelector: (selector) => selector === "#page-content" ? content : null
  };
  const source = fs.readFileSync(path.join(__dirname, "live-integrations.js"), "utf8");
  vm.runInNewContext(source, {
    window,
    document,
    fetch: async () => ({ ok: true, json: async () => ({ integrations: [] }) })
  });

  listeners["netcore:page-rendered"]({ detail: "settings" });
  await new Promise((resolve) => setImmediate(resolve));

  assert.ok(content.querySelector("#integration-settings"), "Settings must show the provider connection panel");
});
