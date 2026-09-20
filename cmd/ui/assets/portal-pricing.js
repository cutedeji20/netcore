(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) { module.exports = api; return; }
  root.NetCorePortalPricing = api;
}(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";
  function breakdown(plan) {
    if (!plan || typeof plan !== "object") return null;
    var price = plan.price_minor, fee = plan.bank_charge_minor, total = plan.total_minor;
    if (!Number.isSafeInteger(price) || price < 0 || !Number.isSafeInteger(fee) || fee < 0 || !Number.isSafeInteger(total) || total !== price + fee) return null;
    return { priceMinor: price, bankChargeMinor: fee, totalMinor: total, currency: typeof plan.currency === "string" ? plan.currency : "NGN" };
  }
  return { breakdown: breakdown };
}));
