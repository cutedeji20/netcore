(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  root.NetCorePortalRegistration = api;
})(typeof window !== "undefined" ? window : globalThis, function () {
  "use strict";

  function validate(email, password, confirmation) {
    if (!String(email || "").trim()) return "Enter your email address.";
    if (String(password || "").length < 12) return "Your password must be at least 12 characters.";
    if (String(password || "").length > 1024) return "Your password is too long. Please use 1024 characters or fewer.";
    if (String(password || "") !== String(confirmation || "")) return "The password confirmation does not match.";
    return "";
  }

  return { validate: validate };
});
