(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = api;
    return;
  }
  root.NetCorePaymentReadiness = api;
}(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  function publicHTTPSURL(value) {
    if (typeof value !== "string" || value.length === 0 || value.length > 2048) return "";
    try {
      var parsed = new URL(value);
      if (parsed.protocol !== "https:" || parsed.username || parsed.password || parsed.hash) return "";
      return parsed.href;
    } catch (_) {
      return "";
    }
  }

  function toDisplay(payload) {
    if (!payload || payload.provider === "disabled" || payload.checkout_status === "DISABLED") {
      return { tone: "disabled", title: "Payments are disabled", rows: [["Provider", "Not configured"]] };
    }
    var callbackURL = publicHTTPSURL(payload.callback_url);
    var webhookURL = publicHTTPSURL(payload.webhook_url);
    if (payload.provider === "paystack" && payload.checkout_status === "READY" && callbackURL && webhookURL) {
      return {
        tone: "ready",
        title: "Paystack is ready for checkout",
        rows: [["Provider", "Paystack"], ["Customer return", callbackURL], ["Webhook receiver", webhookURL]]
      };
    }
    return {
      tone: "attention",
      title: "Payment setup needs attention",
      rows: [["Provider", payload && payload.provider === "paystack" ? "Paystack" : "Unavailable"], ["Checkout", "Not ready"]]
    };
  }

  return { toDisplay: toDisplay };
}));
