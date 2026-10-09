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
      var discountPanel = document.createElement("section"); discountPanel.className = "panel reseller-discount-control";
      var discountTitle = document.createElement("h2"); discountTitle.textContent = "Reseller discount";
      var discountCopy = document.createElement("p"); discountCopy.className = "description";
      discountCopy.textContent = "Resellers receive " + (Number(data.reseller_discount_basis_points || 0) / 100).toFixed(2) + "% off plan prices. The bank charge is added afterward.";
      discountPanel.append(discountTitle, discountCopy); panel.after(discountPanel);
      var principal = window.NETCORE_PRINCIPAL || {};
      if (Array.isArray(principal.permissions) && principal.permissions.indexOf("team.write") !== -1) {
        var discountButton = document.createElement("button"); discountButton.type = "button"; discountButton.className = "button primary"; discountButton.textContent = "Change reseller discount";
        discountButton.addEventListener("click", function () { openDiscountDialog(discountCopy); }); discountPanel.append(discountButton);
      }
      if (!api.canEdit(window.NETCORE_PRINCIPAL)) return;
      var button = document.createElement("button"); button.type = "button"; button.className = "button primary"; button.textContent = "Change bank charge";
      button.addEventListener("click", function () { openDialog(panel, copy); }); panel.append(button);
    }).catch(function (error) { copy.textContent = error.message || "Billing settings could not be loaded."; });
  }
  function openDiscountDialog(copy) {
    var form = document.createElement("form"); form.className = "payment-clear-dialog";
    var heading = document.createElement("h2"); heading.textContent = "Confirm reseller discount";
    var label = document.createElement("label"); label.textContent = "Discount percentage (0–99.99)";
    var percent = document.createElement("input"); percent.type = "number"; percent.min = "0"; percent.max = "99.99"; percent.step = "0.01"; percent.required = true; percent.value = (Number(settings.reseller_discount_basis_points || 0) / 100).toFixed(2); label.append(percent);
    var passwordLabel = document.createElement("label"); passwordLabel.textContent = "Current password";
    var password = document.createElement("input"); password.type = "password"; password.required = true; passwordLabel.append(password);
    var mfaLabel = document.createElement("label"); mfaLabel.textContent = "Authenticator code";
    var mfa = document.createElement("input"); mfa.required = true; mfa.inputMode = "numeric"; mfa.maxLength = 6; mfaLabel.append(mfa);
    var feedback = document.createElement("p"); feedback.className = "payment-clear-feedback";
    var footer = document.createElement("footer"); var cancel = document.createElement("button"); cancel.type = "button"; cancel.className = "button"; cancel.textContent = "Cancel";
    var submit = document.createElement("button"); submit.type = "submit"; submit.className = "button primary"; submit.textContent = "Save securely";
    footer.append(cancel, submit); form.append(heading, label, passwordLabel, mfaLabel, feedback, footer);
    var backdrop = document.createElement("div"); backdrop.className = "payment-clear-dialog-backdrop"; backdrop.append(form); document.body.append(backdrop);
    cancel.addEventListener("click", function () { backdrop.remove(); }); percent.focus();
    form.addEventListener("submit", function (event) {
      event.preventDefault(); var value = Number(percent.value); var points = Math.round(value * 100);
      if (!Number.isFinite(value) || value < 0 || value > 99.99 || Math.abs(points / 100 - value) > 0.000001) { feedback.textContent = "Enter a percentage from 0 to 99.99 with at most two decimal places."; return; }
      submit.disabled = true; feedback.textContent = "Saving securely…";
      fetch(base + "/api/v1/billing/reseller-discount", { method: "PUT", credentials: "include", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ reseller_discount_basis_points: points, password: password.value, mfa_code: mfa.value }) })
        .then(function (r) { return r.json().catch(function () { return {}; }).then(function (b) { if (!r.ok) throw new Error(safeError(b, "Discount could not be saved.")); return b.data; }); })
        .then(function (data) { settings.reseller_discount_basis_points = data.reseller_discount_basis_points; copy.textContent = "Resellers receive " + (Number(data.reseller_discount_basis_points) / 100).toFixed(2) + "% off plan prices. The bank charge is added afterward."; backdrop.remove(); if (window.NetCoreToast) window.NetCoreToast.show("Reseller discount updated."); })
        .catch(function (error) { feedback.textContent = error.message || "Discount could not be saved."; submit.disabled = false; });
    });
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
