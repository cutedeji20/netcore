(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  root.NetCorePortalRegistration = api;
})(typeof window !== "undefined" ? window : globalThis, function () {
  "use strict";

  function validate(email, password, confirmation, phone) {
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(String(email || "").trim())) return "Enter a valid email address.";
    var digits = String(phone || "").replace(/[^0-9]/g, "");
    if (!/^(?:0\d{10}|234\d{10}|[789]\d{9})$/.test(digits)) return "Enter a valid Nigerian mobile phone number.";
    if (String(password || "").length < 4 || String(password || "").length > 12) return "Your password must be 4 to 12 characters.";
    if (!/^[A-Za-z0-9]+$/.test(String(password || ""))) return "Use letters and numbers only for your password.";
    if (String(password || "") !== String(confirmation || "")) return "The password confirmation does not match.";
    return "";
  }

  return { validate: validate };
});
