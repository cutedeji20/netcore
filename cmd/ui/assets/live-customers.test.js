const assert = require("node:assert/strict");
const test = require("node:test");
const { customerPayload, renderCustomerActions, customerErrorMessage, customerMutationRequest, deviceOptionLabel } = require("./live-customers.js");

test("registered devices show a readable full MAC", () => {
  assert.equal(deviceOptionLabel({ label: "MAAL", normalized_mac: "46388ddbb0f9" }), "MAAL · 46:38:8D:DB:B0:F9");
  assert.equal(deviceOptionLabel({ normalized_mac: "d2bd24d25372" }), "Device · D2:BD:24:D2:53:72");
});

test("customer form sends only profile fields", () => {
  assert.deepEqual(customerPayload({
    first_name: "Chika", last_name: "Nwosu", email: "chika@example.test", phone: "+2348031114280", password: "never-send", mfa_code: "123456"
  }), {
    first_name: "Chika", last_name: "Nwosu", email: "chika@example.test", phone: "+2348031114280"
  });
});

test("duplicate email guidance reads the standard API error envelope", () => {
  assert.equal(customerErrorMessage({ error: { code: "CUSTOMER_EMAIL_EXISTS", message: "A customer with this e-mail already exists." } }), "A customer already uses this e-mail. Check the existing customer or use a different address.");
});

test("customer mutation request excludes credentials and uses same-origin JSON", () => {
  const request = customerMutationRequest("/api/v1/customers", "POST", { first_name: "Chika", last_name: "Nwosu", email: "chika@example.test", phone: "", password: "not allowed" });
  assert.equal(request.credentials, "same-origin");
  assert.deepEqual(JSON.parse(request.body), { first_name: "Chika", last_name: "Nwosu", email: "chika@example.test", phone: "" });
});

test("customer actions are absent without customer.write", () => {
  assert.equal(renderCustomerActions({ permissions: ["customer.read"] }).includes("Create customer"), false);
});
