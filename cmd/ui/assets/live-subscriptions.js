(function () {
  "use strict";

  var loadedSubscriptions = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");

  function currentPage() {
    return window.location.hash.slice(1) || "overview";
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

  function setHeadings(table) {
    var headingRow = table.querySelector("thead tr");
    var headings = ["Customer", "Plan", "Starts", "Expires", "Payment", "Service"];
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
      var emptyRow = document.createElement("tr");
      var emptyCell = document.createElement("td");
      emptyCell.colSpan = 6;
      emptyCell.textContent = "No subscriptions match this view.";
      emptyRow.appendChild(emptyCell);
      body.appendChild(emptyRow);
      return;
    }

    loadedSubscriptions.forEach(function (subscription) {
      var row = document.createElement("tr");
      appendCustomerCell(row, subscription.customer || {});
      appendTextCell(row, subscription.plan && subscription.plan.name);
      appendTextCell(row, formatDate(subscription.starts_at));
      appendTextCell(row, formatDate(subscription.expires_at) + (subscription.auto_renew ? " · Auto-renew" : ""));
      appendStatusCell(row, subscription.payment_status);
      appendStatusCell(row, subscription.status);
      body.appendChild(row);
    });
  }

  function requestSubscriptions() {
    if (loadedSubscriptions || requestInFlight) return;
    requestInFlight = true;
    fetch(apiBase + "/api/v1/subscriptions?limit=25", {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) return null;
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) return;
        loadedSubscriptions = payload.data;
        displaySubscriptions();
      })
      .catch(function () {
        // The authorised page remains empty when its API request fails.
      })
      .finally(function () {
        requestInFlight = false;
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "subscriptions") return;
    if (loadedSubscriptions) displaySubscriptions();
    else requestSubscriptions();
  }

  window.addEventListener("netcore:page-rendered", onPageRendered);
  if (currentPage() === "subscriptions") onPageRendered({ detail: "subscriptions" });
}());
