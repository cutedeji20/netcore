(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = api;
    return;
  }
  root.NetCorePortalCheckout = api;
}(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  var storageKey = "netcore.portal.payment.pending.v1";

  function validReference(value) {
    return typeof value === "string" && /^pay-[a-f0-9]{32}$/.test(value);
  }

  function validConnection(value) {
    if (!value || typeof value !== "object") return false;
    var clientMAC = value.client_mac;
    var nasAddress = value.nas_address;
    var loginURL = value.hotspot_login_url;
    if (typeof clientMAC !== "string" || clientMAC.length === 0 || clientMAC.length > 64 || typeof nasAddress !== "string" || nasAddress.length === 0 || nasAddress.length > 255 || typeof loginURL !== "string" || loginURL.length === 0 || loginURL.length > 2048) {
      return false;
    }
    try {
      var parsed = new URL(loginURL);
      return parsed.protocol === "http:" || parsed.protocol === "https:";
    } catch (_) {
      return false;
    }
  }

  function rememberPaymentReturn(storage, reference, connection) {
    if (!validReference(reference) || !validConnection(connection)) return false;
    try {
      storage.setItem(storageKey, JSON.stringify({
        reference: reference,
        connection: {
          client_mac: connection.client_mac,
          nas_address: connection.nas_address,
          hotspot_login_url: connection.hotspot_login_url
        }
      }));
      return true;
    } catch (_) {
      return false;
    }
  }

  function readPaymentReturn(storage) {
    var raw;
    try {
      raw = storage.getItem(storageKey);
    } catch (_) {
      return null;
    }
    if (!raw) return null;
    try {
      var value = JSON.parse(raw);
      if (value && validReference(value.reference) && validConnection(value.connection)) {
        return {
          reference: value.reference,
          connection: {
            client_mac: value.connection.client_mac,
            nas_address: value.connection.nas_address,
            hotspot_login_url: value.connection.hotspot_login_url
          }
        };
      }
    } catch (_) {
      // The storage entry is untrusted browser state and is removed below.
    }
    clearPaymentReturn(storage);
    return null;
  }

  function clearPaymentReturn(storage) {
    try {
      storage.removeItem(storageKey);
    } catch (_) {
      // Private browsing or an exhausted browser store must not break portal use.
    }
  }

  return {
    storageKey: storageKey,
    validReference: validReference,
    rememberPaymentReturn: rememberPaymentReturn,
    readPaymentReturn: readPaymentReturn,
    clearPaymentReturn: clearPaymentReturn
  };
}));
