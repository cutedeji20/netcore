(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  root.NetCorePortalRegistration = api;
})(typeof window !== "undefined" ? window : globalThis, function () {
  "use strict";

  function validate(email, password, confirmation, phone) {
    if (!String(email || "").trim()) return "Enter your email address.";
    if (!/^\+[1-9]\d{7,14}$/.test(String(phone || "").trim())) return "Enter a valid phone number with country code.";
    if (String(password || "").length < 4 || String(password || "").length > 12) return "Your password must be 4 to 12 characters.";
    if (!/^[A-Za-z0-9]+$/.test(String(password || ""))) return "Use letters and numbers only for your password.";
    if (String(password || "") !== String(confirmation || "")) return "The password confirmation does not match.";
    return "";
  }

  return { validate: validate };
});
