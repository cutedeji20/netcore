(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = api;
    return;
  }
  root.NetCoreSessionExpiry = api;
}(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  function arm(expiresAt, options) {
    options = options || {};
    var now = options.now || Date.now;
    var schedule = options.setTimeout || setTimeout;
    var cancel = options.clearTimeout || clearTimeout;
    var onExpired = options.onExpired || function () {};
    var expiresAtMilliseconds = Date.parse(expiresAt);
    if (!Number.isFinite(expiresAtMilliseconds)) {
      onExpired();
      return function () {};
    }
    var delay = expiresAtMilliseconds - now();
    if (delay <= 0) {
      onExpired();
      return function () {};
    }
    var timer = schedule(onExpired, delay);
    return function () { cancel(timer); };
  }

  return { arm: arm };
}));
