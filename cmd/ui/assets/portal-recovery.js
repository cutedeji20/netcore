(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = api;
    return;
  }
  root.NetCorePortalRecovery = api;
}(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  function createChallenge(email, challengeID) {
    email = typeof email === "string" ? email.trim().toLowerCase() : "";
    challengeID = typeof challengeID === "string" ? challengeID.trim() : "";
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email) || challengeID.length < 16 || challengeID.length > 200) {
      return null;
    }
    return { email: email, challengeID: challengeID };
  }

  function canConfirm(password, confirmation) {
    return typeof password === "string" && password.length >= 12 && password === confirmation;
  }

  return {
    createChallenge: createChallenge,
    canConfirm: canConfirm
  };
}));
