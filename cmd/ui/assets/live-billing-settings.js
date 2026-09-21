(function () {
  "use strict";
  var api = window.NetCoreBillingSettings;
  var page = window.NetCoreLivePage;
  if (!api || !page) return;
  var base = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var settings;

  function safeError(body, fallback) { return body && body.error && body.error.message ? body.error.message : fallback; }
  function request() {
    return fetch(base + "/api/v1/billing/settings", { credentials: "include", cache: "no-store" })
      .then(function (r) { return r.json().catch(function () { return {}; }).then(function (b) { if (!r.ok) throw new Error(safeError(b, "Billing settings could not be loaded.")); return b.data; }); });
  }
  function render() {
    if (page.current() !== "billing" && page.current() !== "settings") return;
    var host = document.querySelector("#page-content");
    if (!host || host.querySelector(".bank-charge-control")) return;
    var panel = document.createElement("section"); panel.className = "panel bank-charge-control";
    var title = document.createElement("h2"); title.textContent = "Checkout bank charge";
    var copy = document.createElement("p"); copy.className = "description";
    copy.textContent = "Loading the server-set charge…";
    panel.append(title, copy); host.prepend(panel);
    request().then(function (data) {
      settings = data;
      copy.textContent = "Customers pay this fixed amount in addition to their selected plan: " + api.format(data.fixed_bank_charge_minor) + ".";
      if (!api.canEdit(window.NETCORE_PRINCIPAL)) return;
      var button = document.createElement("button"); button.type = "button"; button.className = "button primary"; button.textContent = "Change bank charge";
      button.addEventListener("click", function () { openDialog(panel, copy); }); panel.append(button);
    }).catch(function (error) { copy.textContent = error.message || "Billing settings could not be loaded."; });
  }
  function openDialog(panel, copy) {
    var form = document.createElement("form"); form.className = "payment-clear-dialog";
    var heading = document.createElement("h2"); heading.textContent = "Confirm bank charge";
    var amountLabel = document.createElement("label"); amountLabel.textContent = "Bank charge (NGN)";
    var amount = document.createElement("input"); amount.required = true; amount.inputMode = "decimal"; amount.value = (Number(settings.fixed_bank_charge_minor || 0) / 100).toFixed(2); amountLabel.append(amount);
    var passwordLabel = document.createElement("label"); passwordLabel.textContent = "Current password";
    var password = document.createElement("input"); password.type = "password"; password.required = true; passwordLabel.append(password);
    var mfaLabel = document.createElement("label"); mfaLabel.textContent = "Authenticator code";
    var mfa = document.createElement("input"); mfa.required = true; mfa.inputMode = "numeric"; mfaLabel.append(mfa);
    var feedback = document.createElement("p"); feedback.className = "payment-clear-feedback";
    var footer = document.createElement("footer"); var cancel = document.createElement("button"); cancel.type = "button"; cancel.className = "button"; cancel.textContent = "Cancel";
    var submit = document.createElement("button"); submit.type = "submit"; submit.className = "button primary"; submit.textContent = "Save securely";
    cancel.addEventListener("click", function () { form.parentNode.remove(); }); footer.append(cancel, submit); form.append(heading, amountLabel, passwordLabel, mfaLabel, feedback, footer);
    var backdrop = document.createElement("div"); backdrop.className = "payment-clear-dialog-backdrop"; backdrop.append(form); document.body.append(backdrop); amount.focus();
    form.addEventListener("submit", function (event) {
      event.preventDefault(); var value = amount.value.trim();
      if (!api.exactNGN(value)) { feedback.textContent = "Enter a non-negative NGN amount with no more than two decimal places."; return; }
      submit.disabled = true; feedback.textContent = "Saving securely…";
      fetch(base + "/api/v1/billing/settings", { method: "PUT", credentials: "include", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ fixed_bank_charge: value, password: password.value, mfa_code: mfa.value }) })
        .then(function (r) { return r.json().catch(function () { return {}; }).then(function (b) { if (!r.ok) throw new Error(safeError(b, "Billing settings could not be saved.")); return b.data; }); })
        .then(function (data) { settings = data; copy.textContent = "Customers pay this fixed amount in addition to their selected plan: " + api.format(data.fixed_bank_charge_minor) + "."; backdrop.remove(); if (window.NetCoreToast) window.NetCoreToast.show("Bank charge updated."); })
        .catch(function (error) { feedback.textContent = error.message || "Billing settings could not be saved."; submit.disabled = false; });
    });
  }
  page.subscribe(function (id) { if (id === "billing") render(); });
}());
