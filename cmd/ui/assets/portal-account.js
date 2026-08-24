(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = api;
    return;
  }
  root.NetCorePortalAccount = api;
}(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  function text(value, maxLength) {
    if (typeof value !== "string") return "";
    value = value.trim();
    return value.length > 0 && value.length <= maxLength ? value : "";
  }

  function timestamp(value) {
    if (typeof value !== "string" || Number.isNaN(Date.parse(value))) return "";
    return value;
  }

  function subscription(value) {
    if (!value || typeof value !== "object") return null;
    var planName = text(value.plan_name, 160);
    var status = text(value.status, 32);
    var paymentStatus = text(value.payment_status, 32);
    if (!planName || !status || !paymentStatus) return null;
    return { planName: planName, status: status, paymentStatus: paymentStatus, startsAt: timestamp(value.starts_at), expiresAt: timestamp(value.expires_at) };
  }

  function payment(value) {
    if (!value || typeof value !== "object") return null;
    var reference = text(value.reference, 160);
    var currency = text(value.currency, 3);
    var status = text(value.status, 32);
    var createdAt = timestamp(value.created_at);
    if (!reference || !currency || !status || !createdAt || !Number.isSafeInteger(value.amount_minor) || value.amount_minor < 0) return null;
    return { reference: reference, amountMinor: value.amount_minor, currency: currency, status: status, createdAt: createdAt };
  }

  function displayModel(response) {
    var data = response && typeof response === "object" ? response.data : null;
    var subscriptions = data && Array.isArray(data.subscriptions) ? data.subscriptions.map(subscription).filter(Boolean) : [];
    var payments = data && Array.isArray(data.payments) ? data.payments.map(payment).filter(Boolean) : [];
    return { subscriptions: subscriptions, payments: payments };
  }

  return { displayModel: displayModel };
}));
