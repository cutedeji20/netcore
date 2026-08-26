(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = api;
    return;
  }
  root.NetCorePortalNavigation = api;
}(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  function destinationAfterSignIn(state) {
    state = state || {};
    if (state.hasReturningPayment === true) return "payment";
    if (state.hasSelectedPlan === true) return "checkout";
    if (state.accountRequested === true || state.hasConnection !== true) return "account";
    return "handoff";
  }

  return { destinationAfterSignIn: destinationAfterSignIn };
}));
