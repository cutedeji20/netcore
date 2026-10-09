(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = api;
    return;
  }
  root.NetCorePortalVouchers = api;
}(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  function normalizeVoucherCode(value) {
    if (value == null) return "";
    return String(value).trim().toUpperCase().replace(/[^A-Z0-9]/g, "");
  }

  function validateVoucherCode(value, options) {
    var raw = value == null ? "" : String(value).trim();
    if (!raw) return "Enter a voucher code.";
    if (!/^[A-Za-z0-9\s-]+$/.test(raw)) {
      return "Voucher codes use letters and numbers only.";
    }
    var code = normalizeVoucherCode(raw);
    if (!/^NC(?:[A-Z2-7]{18}|[A-Z2-7]{32})$/.test(code)) {
      return "Enter a valid voucher code.";
    }
    return "";
  }

  function maskVoucherCode(value) {
    var code = normalizeVoucherCode(value);
    if (!code) return "—";
    if (code.length <= 2) return code.slice(0, 1) + "••••";
    if (code.length <= 6) return code.slice(0, 2) + "••••" + code.slice(-2);
    return code.slice(0, 4) + "••••" + code.slice(-4);
  }

  function guestRequest(code, connection) {
    if (!connection || typeof connection !== "object") return null;
    var normalizedCode = normalizeVoucherCode(code);
    var message = validateVoucherCode(normalizedCode);
    if (message) return null;

    var mac = String(connection.client_mac || "").trim();
    var nas = String(connection.nas_address || "").trim();
    var hotspot = String(connection.hotspot_login_url || "").trim();
    if (!mac || !nas || !hotspot) return null;
    try {
      var parsed = new URL(hotspot);
      if (!/^(http|https):$/.test(parsed.protocol)) return null;
    } catch (_) {
      return null;
    }
    return {
      code: normalizedCode,
      client_mac: mac,
      nas_address: nas,
      hotspot_login_url: hotspot
    };
  }

  return {
    normalizeVoucherCode: normalizeVoucherCode,
    validateVoucherCode: validateVoucherCode,
    maskVoucherCode: maskVoucherCode,
    guestRequest: guestRequest
  };
}));
