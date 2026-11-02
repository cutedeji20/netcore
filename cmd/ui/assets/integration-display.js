(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = api;
    return;
  }
  root.NetCoreIntegrationDisplay = api;
}(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  var providers = [
    { provider: "resend", name: "Resend", fallback: "Email verification and receipts" },
    { provider: "paystack", name: "Paystack", fallback: "Checkout and payment verification" },
    { provider: "squad", name: "Squad", fallback: "Checkout and payment verification" },
    { provider: "palmpay", name: "PalmPay", fallback: "Checkout and payment verification" }
  ];

  function labelStatus(value) {
    if (value === "ACTIVE") return "Active";
    if (value === "DISABLED") return "Disabled";
    return "Disconnected";
  }

  function toCards(items) {
    var configured = {};
    (Array.isArray(items) ? items : []).forEach(function (item) {
      if (item && (item.provider === "resend" || item.provider === "squad" || item.provider === "paystack" || item.provider === "palmpay")) configured[item.provider] = item;
    });
    return providers.map(function (definition) {
      var item = configured[definition.provider];
      if (!item) return { provider: definition.provider, name: definition.name, status: "Disconnected", detail: definition.fallback, action: "Connect", paymentDefault: false };
      var detail = definition.provider === "resend" ? String(item.sender_email || definition.fallback) : (String(item.squad_mode || item.paystack_mode || item.palmpay_mode || "") === "LIVE" ? "Live mode" : "Test mode");
      return { provider: definition.provider, name: definition.name, status: labelStatus(item.status), detail: detail, action: item.status === "ACTIVE" ? "Update" : "Connect", paymentDefault: item.payment_default === true };
    });
  }

  return { toCards: toCards };
}));
