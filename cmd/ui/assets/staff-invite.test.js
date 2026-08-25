const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");
const { acceptanceRequest, invitationToken, prepareRequest } = require("./staff-invite.js");

test("acceptance sends token in POST JSON, never in its URL", async () => {
  const request = await acceptanceRequest("token-value", "long password", "123456");
  assert.equal(request.url, "/api/v1/staff-invitations/complete");
  assert.equal(JSON.parse(request.body).token, "token-value");
  assert.equal(request.url.includes("token-value"), false);
});

test("preparation also sends the fragment token only in JSON POST", () => {
  const request = prepareRequest("token-value");
  assert.equal(request.url, "/api/v1/staff-invitations/prepare");
  assert.equal(request.method, "POST");
  assert.equal(JSON.parse(request.body).token, "token-value");
});

test("invitation token is read from a fragment and immediately removed", () => {
  let replaced = "";
  const token = invitationToken({ hash: "#token=token-value", pathname: "/staff-invite.html", search: "" }, { replaceState: (_, __, value) => { replaced = value; } });
  assert.equal(token, "token-value");
  assert.equal(replaced, "/staff-invite.html");
});

test("browser flow consumes an emitted token fragment and API uri before completion", async () => {
  const rawToken = "A".repeat(43);
  const listeners = {};
  const form = { hidden: true, elements: { password: { value: "long password" }, mfa_code: { value: "123456" } }, reset() { this.elements.password.value = ""; this.elements.mfa_code.value = ""; }, querySelector(selector) { return selector === "button[type=submit]" ? submit : null; }, addEventListener(name, listener) { listeners[name] = listener; } };
  const submit = { disabled: false };
  const elements = {
    "#staff-invite-form": form,
    "#staff-invite-message": { textContent: "" },
    "#staff-invite-error": { textContent: "" },
    "#staff-mfa-setup": { hidden: true },
    "#staff-mfa-key": { value: "" },
    "#staff-mfa-uri": { value: "" }
  };
  const requests = [];
  let replacedPath = "";
  let redirect = "";
  const window = { location: { hash: "#token=" + rawToken, pathname: "/staff-invite.html", search: "", replace(value) { redirect = value; } }, history: { replaceState(_, __, value) { replacedPath = value; } } };
  const source = fs.readFileSync(path.join(__dirname, "staff-invite.js"), "utf8");
  vm.runInNewContext(source, { window, document: { querySelector(selector) { return elements[selector]; } }, fetch: async (url, options) => { requests.push({ url, options }); return requests.length === 1 ? { ok: true, json: async () => ({ mfa_setup: { manual_key: "manual-key", uri: "otpauth://totp/NetCore:test?secret=manual-key" } }) } : { ok: true }; }, Promise });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(replacedPath, "/staff-invite.html");
  assert.deepEqual(JSON.parse(requests[0].options.body), { token: rawToken });
  assert.equal(form.hidden, false);
  assert.equal(elements["#staff-mfa-uri"].value, "otpauth://totp/NetCore:test?secret=manual-key");
  listeners.submit({ preventDefault() {} });
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(JSON.parse(requests[1].options.body), { token: rawToken, password: "long password", mfa_code: "123456" });
  assert.equal(redirect, "/");
  assert.equal(form.elements.password.value, "");
  assert.equal(form.elements.mfa_code.value, "");
  assert.equal(elements["#staff-mfa-key"].value, "");
  assert.equal(elements["#staff-mfa-uri"].value, "");
});
