function subscriptionDeviceLabel(device) {
  if (!device) return "Not assigned";
  var mac = String(device.normalized_mac || "").replace(/[^0-9a-f]/gi, "").toUpperCase();
  var readableMAC = /^[0-9A-F]{12}$/.test(mac) ? mac.match(/.{2}/g).join(":") : "Unknown MAC";
  return String(device.label || "Device") + " · " + readableMAC;
}
function canTransferSubscription(subscription, enabled, permission, now) {
  return Boolean(enabled && permission && subscription && subscription.status === "ACTIVE" && subscription.device && subscription.device.id && Date.parse(subscription.expires_at) > now);
}
if (typeof module !== "undefined" && module.exports) module.exports = { subscriptionDeviceLabel: subscriptionDeviceLabel, canTransferSubscription: canTransferSubscription };

if (typeof window !== "undefined") (function () {
  "use strict";

  var loadedSubscriptions = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var livePage = window.NetCoreLivePage;
  var listConfig = window.NetCoreLiveListConfig.get("subscriptions");
  var listState = window.NetCoreLiveListControls.createState(listConfig.filters, listConfig.initialFilter, listConfig.filterParam);
  var loadedSubscriptionsMeta = {};
  var searchTimer = 0;
  var pendingQuery = "";
  var criteriaPending = false;
  var requestVersion = 0;
  function canRevoke() {
    var principal = window.NETCORE_PRINCIPAL || {};
    return Array.isArray(principal.permissions) && principal.permissions.indexOf("subscription.write") !== -1;
  }

  function currentPage() {
    return livePage.current();
  }

  function showState(state, options) {
    livePage.showState("subscriptions", state, options);
  }

  function safeText(value) {
    return value == null || value === "" ? "—" : String(value);
  }

  function formatDate(value) {
    if (!value) return "—";
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) return "—";
    return date.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
  }

  function initials(customer) {
    var parts = [customer.first_name, customer.last_name].filter(Boolean);
    if (parts.length) {
      return parts.map(function (part) { return part.trim().slice(0, 1).toUpperCase(); }).join("").slice(0, 2);
    }
    return String(customer.customer_number || "?").slice(0, 2).toUpperCase();
  }

  function statusClass(status) {
    if (status === "ACTIVE" || status === "PAID") return "green";
    if (status === "PENDING" || status === "SUSPENDED" || status === "UNPAID" || status === "PARTIAL") return "amber";
    if (status === "CANCELLED") return "red";
    return "gray";
  }

  function statusLabel(status) {
    return safeText(status).replace(/_/g, " ").toLowerCase().replace(/\b\w/g, function (letter) { return letter.toUpperCase(); });
  }

  function appendTextCell(row, value) {
    var cell = document.createElement("td");
    cell.textContent = safeText(value);
    row.appendChild(cell);
  }

  function appendStatusCell(row, status) {
    var cell = document.createElement("td");
    var label = document.createElement("span");
    label.className = "tag " + statusClass(status);
    label.textContent = statusLabel(status);
    cell.appendChild(label);
    row.appendChild(cell);
  }

  function appendCustomerCell(row, customer) {
    var cell = document.createElement("td");
    var entity = document.createElement("div");
    var mark = document.createElement("span");
    var copy = document.createElement("span");
    var name = document.createElement("strong");
    var detail = document.createElement("small");
    var fullName = [customer.first_name, customer.last_name].filter(Boolean).join(" ").trim() || customer.customer_number;

    entity.className = "entity";
    mark.className = "entity-mark";
    mark.textContent = initials(customer);
    name.textContent = safeText(fullName);
    detail.textContent = safeText(customer.customer_number);
    copy.append(name, detail);
    entity.append(mark, copy);
    cell.appendChild(entity);
    row.appendChild(cell);
  }

  function transferDialog(subscription) {
    var backdrop = document.createElement("div"), form = document.createElement("form"), feedback = document.createElement("p");
    backdrop.className = "customer-dialog-backdrop"; form.className = "customer-dialog";
    var title = document.createElement("h2"); title.textContent = "Transfer plan to another device";
    var summary = document.createElement("p"); summary.textContent = "This moves the existing plan. The old device loses access; expiry and used quota do not reset.";
    var current = document.createElement("p"); current.textContent = "Current: " + subscriptionDeviceLabel(subscription.device) + " · Expires: " + formatDate(subscription.expires_at) + " · Remaining quota: " + (subscription.remaining_bytes == null ? "Unmetered or unavailable" : Number(subscription.remaining_bytes).toLocaleString() + " bytes");
    var target = document.createElement("select"), reason = document.createElement("input"), password = document.createElement("input"), mfa = document.createElement("input");
    reason.required = true; reason.maxLength = 240; password.required = true; password.type = "password"; mfa.required = true; mfa.inputMode = "numeric";
    [["New registered device", target], ["Reason", reason], ["Your password", password], ["Authenticator code", mfa]].forEach(function (pair) {
      var label = document.createElement("label"); label.className = "customer-field"; label.append(document.createTextNode(pair[0]), pair[1]); form.appendChild(label);
    });
    var registerMAC = document.createElement("input"), registerLabel = document.createElement("input"), register = document.createElement("button");
    registerMAC.placeholder = "New device MAC"; registerLabel.placeholder = "Device label (optional)";
    register.type = "button"; register.className = "button"; register.textContent = "Register device first";
    var registration = document.createElement("div"); registration.append(registerMAC, registerLabel, register);
    var cancel = document.createElement("button"), submit = document.createElement("button");
    cancel.type = "button"; cancel.className = "button"; cancel.textContent = "Cancel"; cancel.onclick = function () { backdrop.remove(); };
    submit.type = "submit"; submit.className = "button primary"; submit.textContent = "Transfer existing plan"; submit.disabled = true;
    form.prepend(title, summary, current); form.append(registration, feedback, cancel, submit); backdrop.appendChild(form); document.body.appendChild(backdrop);
    function loadDevices() {
      return fetch(apiBase + "/api/v1/customers/" + encodeURIComponent(subscription.customer.id) + "/devices", { credentials: "same-origin", cache: "no-store" })
        .then(function (response) { if (!response.ok) throw new Error("Devices could not be loaded."); return response.json(); })
        .then(function (body) {
          target.replaceChildren();
          (body.data || []).filter(function (device) { return device.status === "ACTIVE" && device.id !== subscription.device.id; }).forEach(function (device) {
            var option = document.createElement("option"); option.value = device.id; option.textContent = subscriptionDeviceLabel(device); target.appendChild(option);
          });
          submit.disabled = !target.options.length;
          if (!target.options.length) feedback.textContent = "Register the new MAC to this customer before transferring.";
        });
    }
    loadDevices().catch(function (error) { feedback.textContent = error.message; });
    register.onclick = function () {
      if (!registerMAC.value.trim()) { feedback.textContent = "Enter the MAC verified on the AP and MikroTik."; return; }
      register.disabled = true; feedback.textContent = "";
      fetch(apiBase + "/api/v1/customers/" + encodeURIComponent(subscription.customer.id) + "/devices", {
        method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ mac: registerMAC.value.trim(), label: registerLabel.value.trim() })
      }).then(function (response) { return response.json().then(function (body) { if (!response.ok) throw new Error((body.error && body.error.message) || "Device registration failed."); }); })
        .then(loadDevices).catch(function (error) { feedback.textContent = error.message; }).finally(function () { register.disabled = false; });
    };
    form.onsubmit = function (event) {
      event.preventDefault(); if (!target.value || !window.confirm("Move this active plan to " + target.options[target.selectedIndex].textContent + "? The old device will lose access.")) return;
      submit.disabled = true; feedback.textContent = "";
      fetch(apiBase + "/api/v1/subscriptions/" + encodeURIComponent(subscription.id) + "/transfer-device", {
        method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ target_device_id: target.value, reason: reason.value, password: password.value, mfa_code: mfa.value })
      }).then(function (response) { return response.json().then(function (body) { if (!response.ok) throw new Error((body.error && body.error.message) || "Transfer failed."); }); })
        .then(function () { backdrop.remove(); requestSubscriptions(true); }).catch(function (error) { feedback.textContent = error.message; })
        .finally(function () { submit.disabled = false; });
    };
  }

  function setHeadings(table) {
    var headingRow = table.querySelector("thead tr");
    var headings = ["Customer", "Plan", "Device", "Starts", "Expires", "Payment", "Service", "Action"];
    headingRow.replaceChildren();
    headings.forEach(function (value) {
      var heading = document.createElement("th");
      heading.textContent = value;
      headingRow.appendChild(heading);
    });
  }

  function displaySubscriptions() {
    if (!loadedSubscriptions || currentPage() !== "subscriptions") return;
    var table = document.querySelector("#page-content .data-table");
    if (!table) return;

    setHeadings(table);
    var body = table.querySelector("tbody");
    body.replaceChildren();
    if (loadedSubscriptions.length === 0) {
      showState("empty", { message: "No subscriptions match this view." });
      return;
    }

    loadedSubscriptions.forEach(function (subscription) {
      var row = document.createElement("tr");
      appendCustomerCell(row, subscription.customer || {});
      appendTextCell(row, subscription.plan && subscription.plan.name);
      appendTextCell(row, subscriptionDeviceLabel(subscription.device));
      appendTextCell(row, formatDate(subscription.starts_at));
      appendTextCell(row, formatDate(subscription.expires_at) + (subscription.auto_renew ? " · Auto-renew" : ""));
      appendStatusCell(row, subscription.payment_status);
      appendStatusCell(row, subscription.status);
      var action = document.createElement("td");
      var hasAction = false;
      if (canRevoke() && subscription.payment_status === "GRANTED" && (subscription.status === "ACTIVE" || subscription.status === "SUSPENDED")) {
        var button = document.createElement("button");
        button.type = "button"; button.className = "button"; button.textContent = "Revoke grant";
        button.addEventListener("click", function () {
          var reason = window.prompt("Revoke this staff-granted plan? This ends future authorisation. Enter a reason (up to 240 characters):");
          if (!reason || !reason.trim()) return;
          if (reason.trim().length > 240) { window.alert("Reason must be 240 characters or fewer."); return; }
          button.disabled = true;
          fetch(apiBase + "/api/v1/subscriptions/" + encodeURIComponent(subscription.id) + "/revoke-grant", {
            method: "POST", credentials: "include", cache: "no-store", headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ reason: reason.trim() })
          }).then(function (response) { return response.json().catch(function () { return {}; }).then(function (body) { if (!response.ok) throw new Error((body.error && body.error.message) || "Grant could not be revoked."); }); })
            .then(function () { if (window.NetCoreToast) window.NetCoreToast.show("Staff grant revoked."); requestSubscriptions(true); })
            .catch(function (error) { window.alert(error.message || "Grant could not be revoked."); })
            .finally(function () { button.disabled = false; });
        });
        action.appendChild(button);
        hasAction = true;
      }
      if (canTransferSubscription(subscription, loadedSubscriptionsMeta.device_replacement_enabled, canRevoke(), Date.now())) {
        var transferButton = document.createElement("button"); transferButton.type = "button"; transferButton.className = "button"; transferButton.textContent = "Transfer device";
        transferButton.onclick = function () { transferDialog(subscription); };
        action.appendChild(transferButton);
        hasAction = true;
      }
      if (!hasAction) action.textContent = "—";
      row.appendChild(action);
      body.appendChild(row);
    });
    showState("records");
  }

  function filterOptions() {
    return listConfig.filters.map(function (filter) { return { value: filter, label: filter || "All" }; });
  }

  function renderControls() {
    if (currentPage() !== "subscriptions") return;
    livePage.renderListControls("subscriptions", {
      query: criteriaPending ? pendingQuery : listState.query, filter: listState.filter, filters: filterOptions(), busy: requestInFlight,
      hasPrevious: listState.previousCursors.length > 0, hasNext: listState.hasMore,
      searchPlaceholder: "Search subscriptions", searchLabel: "Search subscriptions", filterLabel: "Filter subscriptions",
      onSearch: function (query) {
        clearTimeout(searchTimer);
        pendingQuery = query;
        criteriaPending = true;
        requestVersion += 1;
        window.NetCoreLiveListControls.applyCriteria(listState, query, listState.filter);
        searchTimer = setTimeout(function () { criteriaPending = false; requestVersion += 1; requestSubscriptions(true); }, 250);
      },
      onFilter: function (filter) { applyCriteria(listState.query, filter); },
      onNext: function () { if (criteriaPending) return; if (window.NetCoreLiveListControls.nextPage(listState)) requestSubscriptions(true); else renderControls(); },
      onPrevious: function () { if (criteriaPending) return; if (listState.previousCursors.length) { window.NetCoreLiveListControls.previousPage(listState); requestSubscriptions(true); } else renderControls(); }
    });
  }

  function applyCriteria(query, filter) {
    if (window.NetCoreLiveListControls.applyCriteria(listState, query, filter)) requestSubscriptions(true);
  }

  function requestSubscriptions(force) {
    if (requestInFlight || (loadedSubscriptions && !force)) return;
    requestInFlight = true;
    var requestVersionAtStart = requestVersion;
    renderControls();
    if (!loadedSubscriptions) showState("loading");
    fetch(window.NetCoreLiveListControls.requestURL(apiBase, listConfig.endpoint, listState, 25), {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) throw new Error("Subscriptions request failed");
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) throw new Error("Subscriptions response was invalid");
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        loadedSubscriptions = payload.data;
        loadedSubscriptionsMeta = payload.meta || {};
        window.NetCoreLiveListControls.applyResponseMeta(listState, loadedSubscriptionsMeta);
        displaySubscriptions();
      })
      .catch(function () {
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        // Last verified records remain visible when a refresh fails.
        if (loadedSubscriptions) displaySubscriptions();
        showState("error", { message: "Subscriptions could not be loaded. Please try again.", preserve: Boolean(loadedSubscriptions), retry: function () { requestSubscriptions(true); } });
      })
      .finally(function () {
        requestInFlight = false;
        renderControls();
        if (!criteriaPending && requestVersionAtStart !== requestVersion) requestSubscriptions(true);
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "subscriptions") return;
    renderControls();
    if (loadedSubscriptions) requestSubscriptions(true);
    else requestSubscriptions();
  }

  livePage.subscribe(function (page) { onPageRendered({ detail: page }); });
}());
