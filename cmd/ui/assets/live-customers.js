function customerPayload(values) {
  return { first_name: String(values.first_name || "").trim(), last_name: String(values.last_name || "").trim(), email: String(values.email || "").trim(), phone: String(values.phone || "").trim() };
}
function renderCustomerActions(principal) {
  var permissions = principal && Array.isArray(principal.permissions) ? principal.permissions : [];
  return permissions.indexOf("customer.write") === -1 ? "" : '<button class="button primary customer-create" type="button">Create customer</button>';
}
function customerErrorMessage(body) {
  if (body && body.error && body.error.code === "CUSTOMER_EMAIL_EXISTS") return "A customer already uses this e-mail. Check the existing customer or use a different address.";
  return body && body.error && typeof body.error.message === "string" ? body.error.message : "The customer change could not be completed. Please try again.";
}
function customerMutationRequest(path, method, values) {
  return { url: path, method: method, credentials: "same-origin", cache: "no-store", headers: { "Content-Type": "application/json", "Accept": "application/json" }, body: JSON.stringify(customerPayload(values)) };
}
if (typeof module !== "undefined" && module.exports) module.exports = { customerPayload: customerPayload, renderCustomerActions: renderCustomerActions, customerErrorMessage: customerErrorMessage, customerMutationRequest: customerMutationRequest };

if (typeof window !== "undefined") (function () {
  "use strict";

  var loadedCustomers = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var livePage = window.NetCoreLivePage;

  function canWrite() { var principal = window.NETCORE_PRINCIPAL || {}; return Array.isArray(principal.permissions) && principal.permissions.indexOf("customer.write") !== -1; }
  function request(path, method, payload) { var requestValue = customerMutationRequest(path, method, payload || {}); return fetch(apiBase + requestValue.url, requestValue); }
  function errorMessage(response) { return response.json().then(customerErrorMessage).catch(function () { return customerErrorMessage(null); }); }
  function customerDialog(title, customer, method, path) {
    var backdrop = document.createElement("div"), form = document.createElement("form"), feedback = document.createElement("p"), submit = document.createElement("button");
    backdrop.className = "customer-dialog-backdrop"; form.className = "customer-dialog"; form.innerHTML = "<h2></h2><p>Customer profiles contain contact details only.</p>";
    form.querySelector("h2").textContent = title;
    [["First name", "first_name"], ["Last name", "last_name"], ["E-mail", "email"], ["Phone (optional)", "phone"]].forEach(function (field) { var label = document.createElement("label"), input = document.createElement("input"); label.className = "customer-field"; label.appendChild(document.createTextNode(field[0])); input.name = field[1]; input.type = field[1] === "email" ? "email" : "text"; input.required = field[1] !== "phone"; input.value = customer && customer[field[1]] || ""; label.appendChild(input); form.appendChild(label); });
    feedback.className = "customer-form-feedback"; var cancel = document.createElement("button"); cancel.type = "button"; cancel.className = "button"; cancel.textContent = "Cancel"; cancel.onclick = function () { backdrop.remove(); }; submit.type = "submit"; submit.className = "button primary"; submit.textContent = title; form.append(feedback, cancel, submit); backdrop.appendChild(form); document.body.appendChild(backdrop);
    form.addEventListener("submit", function (event) { event.preventDefault(); if (submit.disabled) return; submit.disabled = true; feedback.textContent = ""; request(path, method, Object.fromEntries(new FormData(form))).then(function (response) { return response.ok ? undefined : errorMessage(response).then(Promise.reject.bind(Promise)); }).then(function () { form.reset(); backdrop.remove(); requestCustomers(true); }).catch(function (message) { feedback.textContent = message; }).finally(function () { submit.disabled = false; }); });
  }
  function bindHeaderAction() { if (!canWrite() || currentPage() !== "customers") return; var actions = document.querySelector("#page-content .heading-actions"); if (!actions || actions.querySelector(".customer-create")) return; actions.insertAdjacentHTML("beforeend", renderCustomerActions(window.NETCORE_PRINCIPAL)); actions.querySelector(".customer-create").onclick = function () { customerDialog("Create customer", null, "POST", "/api/v1/customers"); }; }

  function currentPage() {
    return livePage.current();
  }

  function showState(state, options) {
    livePage.showState("customers", state, options);
  }

  function safeText(value) {
    return value == null || value === "" ? "—" : String(value);
  }

  function initials(firstName, lastName, fallback) {
    var parts = [firstName, lastName].filter(Boolean);
    if (parts.length) {
      return parts.map(function (part) { return part.trim().slice(0, 1).toUpperCase(); }).join("").slice(0, 2);
    }
    return String(fallback || "?").slice(0, 2).toUpperCase();
  }

  function statusClass(status) {
    if (status === "ACTIVE") return "green";
    if (status === "SUSPENDED") return "amber";
    return "gray";
  }

  function statusLabel(status) {
    if (status === "ACTIVE") return "Active";
    if (status === "SUSPENDED") return "Suspended";
    if (status === "CLOSED") return "Closed";
    return safeText(status);
  }

  function appendTextCell(row, value) {
    var cell = document.createElement("td");
    cell.textContent = safeText(value);
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
    var contact = customer.phone || customer.email || "No contact recorded";

    entity.className = "entity";
    mark.className = "entity-mark";
    mark.textContent = initials(customer.first_name, customer.last_name, customer.customer_number);
    name.textContent = fullName;
    detail.textContent = customer.customer_number + " · " + contact;
    copy.append(name, detail);
    entity.append(mark, copy);
    cell.appendChild(entity);
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

  function displayCustomers() {
    if (!loadedCustomers || currentPage() !== "customers") return;
    var table = document.querySelector("#page-content .data-table");
    if (!table) return;

    var headings = ["Customer", "Phone", "Email", "Joined", "Status"];
    if (canWrite()) headings.push("Actions");
    var headingRow = table.querySelector("thead tr");
    headingRow.replaceChildren();
    headings.forEach(function (value) { var heading = document.createElement("th"); heading.textContent = value; headingRow.appendChild(heading); });

    var body = table.querySelector("tbody");
    body.replaceChildren();
    if (loadedCustomers.length === 0) {
      showState("empty", { message: "No customers match this view." });
      return;
    }
    loadedCustomers.forEach(function (customer) {
      var row = document.createElement("tr");
      appendCustomerCell(row, customer);
      appendTextCell(row, customer.phone);
      appendTextCell(row, customer.email);
      appendTextCell(row, new Date(customer.created_at).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }));
      appendStatusCell(row, customer.status);
      if (canWrite()) { var actions = document.createElement("td"); var edit = document.createElement("button"); edit.type = "button"; edit.className = "button customer-row-action"; edit.textContent = "Edit"; edit.onclick = function () { customerDialog("Edit customer", customer, "PUT", "/api/v1/customers/" + customer.id); }; var deactivate = document.createElement("button"); deactivate.type = "button"; deactivate.className = "button customer-row-action"; deactivate.textContent = "Deactivate"; deactivate.onclick = function () { if (!window.confirm("Deactivate this customer?")) return; deactivate.disabled = true; fetch(apiBase + "/api/v1/customers/" + customer.id + "/deactivate", { method: "POST", credentials: "same-origin", cache: "no-store" }).then(function (response) { return response.ok ? undefined : errorMessage(response).then(Promise.reject.bind(Promise)); }).then(function () { requestCustomers(true); }).catch(function (message) { window.alert(message); }).finally(function () { deactivate.disabled = false; }); }; actions.append(edit, deactivate); row.appendChild(actions); }
      body.appendChild(row);
    });
    showState("records");
  }

  function requestCustomers(force) {
    if (requestInFlight || (loadedCustomers && !force)) return;
    requestInFlight = true;
    if (!loadedCustomers) showState("loading");
    fetch(apiBase + "/api/v1/customers?limit=25", {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) throw new Error("Customers request failed");
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) throw new Error("Customers response was invalid");
        loadedCustomers = payload.data;
        displayCustomers();
      })
      .catch(function () {
        // Last verified records remain visible when a refresh fails.
        if (loadedCustomers) displayCustomers();
        showState("error", { message: "Customers could not be loaded. Please try again.", preserve: Boolean(loadedCustomers), retry: function () { requestCustomers(true); } });
      })
      .finally(function () {
        requestInFlight = false;
      });
  }

  function onPageRendered(event) {
    var page = event.detail;
    if (page !== "customers") return;
    bindHeaderAction();
    if (loadedCustomers) requestCustomers(true);
    else requestCustomers();
  }

  livePage.subscribe(function (page) { onPageRendered({ detail: page }); });
  if (currentPage() === "customers") onPageRendered({ detail: "customers" });
}());
