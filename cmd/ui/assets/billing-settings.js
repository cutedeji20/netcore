(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) { module.exports = api; return; }
  root.NetCoreBillingSettings = api;
}(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";
  function canEdit(principal) { return !!(principal && Array.isArray(principal.permissions) && principal.permissions.indexOf("billing.write") !== -1); }
  function exactNGN(value) { return typeof value === "string" && /^\d+(?:\.\d{1,2})?$/.test(value.trim()); }
  function format(minor) { return "NGN " + (Number(minor || 0) / 100).toFixed(2); }
  return { canEdit: canEdit, exactNGN: exactNGN, format: format };
}));
