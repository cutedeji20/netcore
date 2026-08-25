function invitationToken(locationValue, historyValue) {
  var fragment = String(locationValue.hash || "").replace(/^#/, "");
  var match = /(?:^|&)token=([^&]*)/.exec(fragment);
  var token = "";
  try { token = match ? decodeURIComponent(match[1]) : ""; } catch (_) { token = ""; }
  historyValue.replaceState(null, "", String(locationValue.pathname || "/staff-invite.html") + String(locationValue.search || ""));
  return token;
}

function acceptanceRequest(token, password, mfaCode) {
  return Promise.resolve({
    url: "/api/v1/staff-invitations/complete",
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", "Accept": "application/json" },
    body: JSON.stringify({ token: token, password: password, mfa_code: mfaCode })
  });
}
function prepareRequest(token) {
  return { url: "/api/v1/staff-invitations/prepare", method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json", "Accept": "application/json" }, body: JSON.stringify({ token: token }) };
}

if (typeof module !== "undefined" && module.exports) module.exports = { acceptanceRequest: acceptanceRequest, invitationToken: invitationToken, prepareRequest: prepareRequest };

if (typeof window !== "undefined") (function () {
  "use strict";
  var genericError = "This invitation is invalid or has expired.";
  var token = invitationToken(window.location, window.history);
  var form = document.querySelector("#staff-invite-form");
  var message = document.querySelector("#staff-invite-message");
  var error = document.querySelector("#staff-invite-error");
  var setup = document.querySelector("#staff-mfa-setup");
  var key = document.querySelector("#staff-mfa-key");
  var uri = document.querySelector("#staff-mfa-uri");

  function invalid() { message.textContent = genericError; error.textContent = genericError; form.hidden = true; setup.hidden = true; }
  if (!token) { invalid(); return; }
  fetch(prepareRequest(token).url, prepareRequest(token)).then(function (response) { if (!response.ok) throw new Error("invalid"); return response.json(); }).then(function (payload) {
    var mfa = payload && payload.mfa_setup;
    if (!mfa || !mfa.manual_key || !mfa.uri) throw new Error("invalid");
    key.value = mfa.manual_key; uri.value = mfa.uri; message.textContent = "Set up your authenticator, then choose your password."; setup.hidden = false; form.hidden = false;
  }).catch(invalid);
  form.addEventListener("submit", function (event) {
    event.preventDefault(); var submit = form.querySelector("button[type=submit]"); if (submit.disabled) return; submit.disabled = true; error.textContent = "";
    acceptanceRequest(token, form.elements.password.value, form.elements.mfa_code.value).then(function (requestValue) { return fetch(requestValue.url, requestValue); }).then(function (response) { if (!response.ok) throw new Error("invalid"); form.reset(); key.value = ""; uri.value = ""; window.location.replace("/"); }).catch(invalid).finally(function () { submit.disabled = false; });
  });
}());
