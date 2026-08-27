const test = require("node:test");
const assert = require("node:assert/strict");

test("criteria reset cursor history and reject an unknown filter", () => {
  const controls = require("./live-list-controls.js");
  let state = controls.createState(["", "ACTIVE", "SUSPENDED"], "ACTIVE", "status");
  state.cursor = "opaque-page-one";
  state.previousCursors = [""];
  state.nextCursor = "opaque-page-two";
  state.hasMore = true;

  assert.equal(controls.applyCriteria(state, "  Amina  ", "UNSAFE"), true);
  assert.equal(state.query, "Amina");
  assert.equal(state.filter, "");
  assert.equal(state.cursor, "");
  assert.deepEqual(state.previousCursors, []);
  assert.equal(state.hasMore, false);
});

test("request URLs encode only allowed criteria and opaque cursors", () => {
  const controls = require("./live-list-controls.js");
  const state = controls.createState(["", "ACTIVE"], "ACTIVE", "status");
  state.query = "Amina & Sons";
  state.cursor = "cursor+/=";
  assert.equal(
    controls.requestURL("https://hotspot.example", "/api/v1/subscriptions", state, 25),
    "https://hotspot.example/api/v1/subscriptions?limit=25&q=Amina+%26+Sons&status=ACTIVE&cursor=cursor%2B%2F%3D"
  );
});

test("next and previous use only API-provided cursor state", () => {
  const controls = require("./live-list-controls.js");
  const state = controls.createState([""], "", "");
  controls.applyResponseMeta(state, { has_more: true, next_cursor: "next-one" });
  assert.equal(controls.nextPage(state), "next-one");
  assert.deepEqual(state.previousCursors, [""]);
  assert.equal(controls.previousPage(state), "");
  assert.deepEqual(state.previousCursors, []);
  controls.applyResponseMeta(state, { has_more: true });
  assert.equal(state.hasMore, false);
});

test("configuration exposes only the documented server filters", () => {
  const config = require("./live-list-config.js");
  assert.deepEqual(config.get("subscriptions"), { endpoint: "/api/v1/subscriptions", filterParam: "status", filters: ["", "PENDING", "ACTIVE", "SUSPENDED", "EXPIRED", "CANCELLED"], initialFilter: "" });
  assert.deepEqual(config.get("sessions").filters, ["", "ACTIVE", "SUSPECT", "CLOSED"]);
  assert.deepEqual(config.get("vouchers"), { endpoint: "/api/v1/vouchers/batches", filterParam: "", filters: [""], initialFilter: "" });
  assert.deepEqual(config.get("billing").filters, ["", "PAYMENT", "INVOICE"]);
});
