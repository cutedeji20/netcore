(function () {
  "use strict";

  var loadedSessions = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");

  function currentPage() {
    return window.location.hash.slice(1) || "overview";
  }

  function safeText(value) {
    return value == null || value === "" ? "—" : String(value);
  }

  function formatBytes(value) {
    var bytes;
    try {
      bytes = BigInt(String(value == null || value === "" ? "0" : value));
    } catch (_) {
      return "—";
    }
    if (bytes < 0n) return "—";
    var units = ["B", "KB", "MB", "GB", "TB", "PB"];
    var divisor = 1n;
    var unit = 0;
    while (unit < units.length - 1 && bytes >= divisor * 1024n) {
      divisor *= 1024n;
      unit += 1;
    }
    var whole = bytes / divisor;
    var remainder = bytes % divisor;
    var tenths = remainder * 10n / divisor;
    return whole.toString() + (unit > 0 && tenths > 0n ? "." + tenths.toString() : "") + " " + units[unit];
  }

  function formatDuration(startedAt) {
    var started = new Date(startedAt);
    var seconds = Math.max(0, Math.floor((Date.now() - started.getTime()) / 1000));
    if (!Number.isFinite(seconds)) return "—";
    var days = Math.floor(seconds / 86400);
    var hours = Math.floor(seconds % 86400 / 3600);
    var minutes = Math.floor(seconds % 3600 / 60);
    if (days) return days + "d " + hours + "h";
    if (hours) return hours + "h " + minutes + "m";
    return minutes + "m";
  }

  function initials(customer) {
    var parts = [customer.first_name, customer.last_name].filter(Boolean);
    if (parts.length) {
      return parts.map(function (part) { return part.trim().slice(0, 1).toUpperCase(); }).join("").slice(0, 2);
    }
    return String(customer.customer_number || "?").slice(0, 2).toUpperCase();
  }

  function statusClass(status) {
    if (status === "ACTIVE") return "green";
    if (status === "SUSPECT") return "amber";
    return "gray";
  }

  function statusLabel(status) {
    return safeText(status).toLowerCase().replace(/\b\w/g, function (letter) { return letter.toUpperCase(); });
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

  function appendCustomerCell(row, session) {
    var customer = session.customer || {};
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
    detail.textContent = safeText(session.ip_address || customer.customer_number);
    copy.append(name, detail);
    entity.append(mark, copy);
    cell.appendChild(entity);
    row.appendChild(cell);
  }

  function setHeadings(table) {
    var headings = ["Customer", "Router", "Started", "Usage", "Service"];
    var headingRow = table.querySelector("thead tr");
    headingRow.replaceChildren();
    headings.forEach(function (value) {
      var heading = document.createElement("th");
      heading.textContent = value;
      headingRow.appendChild(heading);
    });
  }

  function usageText(session) {
    var usage = session.usage || {};
    if (usage.consumed_bytes !== "" && usage.consumed_bytes != null && usage.quota_bytes !== "" && usage.quota_bytes != null) {
      return formatBytes(usage.consumed_bytes) + " / " + formatBytes(usage.quota_bytes);
    }
    return formatBytes(session.session_bytes) + " this session";
  }

  function displaySessions() {
    if (!loadedSessions || currentPage() !== "sessions") return;
    var table = document.querySelector("#page-content .data-table");
    if (!table) return;

    setHeadings(table);
    var body = table.querySelector("tbody");
    body.replaceChildren();
    if (loadedSessions.length === 0) {
      var emptyRow = document.createElement("tr");
      var emptyCell = document.createElement("td");
      emptyCell.colSpan = 5;
      emptyCell.textContent = "No active sessions match this view.";
      emptyRow.appendChild(emptyCell);
      body.appendChild(emptyRow);
      return;
    }

    loadedSessions.forEach(function (session) {
      var row = document.createElement("tr");
      appendCustomerCell(row, session);
      appendTextCell(row, session.router && session.router.name);
      appendTextCell(row, formatDuration(session.started_at));
      appendTextCell(row, usageText(session));
      appendStatusCell(row, session.status);
      body.appendChild(row);
    });
  }

  function requestSessions() {
    if (loadedSessions || requestInFlight) return;
    requestInFlight = true;
    fetch(apiBase + "/api/v1/sessions?limit=25&status=ACTIVE", {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) return null;
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) return;
        loadedSessions = payload.data;
        displaySessions();
      })
      .catch(function () {
        // The authorised page remains empty when its API request fails.
      })
      .finally(function () {
        requestInFlight = false;
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "sessions") return;
    if (loadedSessions) displaySessions();
    else requestSessions();
  }

  window.addEventListener("netcore:page-rendered", onPageRendered);
  if (currentPage() === "sessions") onPageRendered({ detail: "sessions" });
}());
