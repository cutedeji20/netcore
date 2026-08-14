(function () {
  "use strict";

  var loadedCustomers = null;
  var requestInFlight = false;
  var apiBase = window.NETCORE_API_URL || "http://127.0.0.1:8080";

  function currentPage() {
    return window.location.hash.slice(1) || "overview";
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
    table.querySelectorAll("thead th").forEach(function (heading, index) {
      heading.textContent = headings[index] || "";
    });

    var body = table.querySelector("tbody");
    body.replaceChildren();
    loadedCustomers.forEach(function (customer) {
      var row = document.createElement("tr");
      appendCustomerCell(row, customer);
      appendTextCell(row, customer.phone);
      appendTextCell(row, customer.email);
      appendTextCell(row, new Date(customer.created_at).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }));
      appendStatusCell(row, customer.status);
      body.appendChild(row);
    });
  }

  function requestCustomers() {
    if (loadedCustomers || requestInFlight) return;
    requestInFlight = true;
    fetch(apiBase + "/api/v1/customers?limit=25", {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) return null;
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) return;
        loadedCustomers = payload.data;
        displayCustomers();
      })
      .catch(function () {
        // The unauthenticated or offline preview remains useful by design.
      })
      .finally(function () {
        requestInFlight = false;
      });
  }

  function onPageRendered(event) {
    var page = event.detail;
    if (page !== "customers") return;
    if (loadedCustomers) displayCustomers();
    else requestCustomers();
  }

  window.addEventListener("netcore:page-rendered", onPageRendered);
  if (currentPage() === "customers") onPageRendered({ detail: "customers" });
}());
