(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  if (root) root.NetCoreLiveListConfig = api;
})(typeof window !== "undefined" ? window : typeof globalThis !== "undefined" ? globalThis : this, function () {
  var configs = Object.freeze({
    subscriptions: { endpoint: "/api/v1/subscriptions", filterParam: "status", filters: ["", "PENDING", "ACTIVE", "SUSPENDED", "EXPIRED", "CANCELLED"], initialFilter: "" },
    sessions: { endpoint: "/api/v1/sessions", filterParam: "status", filters: ["", "ACTIVE", "SUSPECT", "CLOSED"], initialFilter: "ACTIVE" },
    vouchers: { endpoint: "/api/v1/vouchers/batches", filterParam: "", filters: [""], initialFilter: "" },
    network: { endpoint: "/api/v1/network/routers", filterParam: "status", filters: ["", "PROVISIONING", "ONLINE", "OFFLINE", "RETIRED"], initialFilter: "" },
    billing: { endpoint: "/api/v1/billing/transactions", filterParam: "source", filters: ["", "PAYMENT", "INVOICE"], initialFilter: "" },
    security: { endpoint: "/api/v1/security/events", filterParam: "", filters: [""], initialFilter: "" },
    automations: { endpoint: "/api/v1/automations", filterParam: "status", filters: ["", "DRAFT", "READY", "PAUSED"], initialFilter: "" }
  });

  function get(page) {
    var item = configs[page];
    return item ? { endpoint: item.endpoint, filterParam: item.filterParam, filters: item.filters.slice(), initialFilter: item.initialFilter } : null;
  }

  return { get: get };
});
