(function () {
  "use strict";

  var loadedPlans = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var currencyExponents = {
    JPY: 0, KRW: 0, VND: 0, CLP: 0, ISK: 0, XAF: 0, XOF: 0,
    BHD: 3, KWD: 3, OMR: 3, TND: 3, JOD: 3
  };

  function currentPage() {
    return window.location.hash.slice(1) || "overview";
  }

  function safeText(value) {
    return value == null || value === "" ? "—" : String(value);
  }

  function formatRate(bps) {
    var megabits = Number(bps) / 1000000;
    if (!Number.isFinite(megabits)) return "—";
    return (Number.isInteger(megabits) ? megabits : megabits.toFixed(1)) + " Mbps";
  }

  function formatDuration(seconds) {
    var days = Math.round(Number(seconds) / 86400);
    return Number.isFinite(days) && days > 0 ? days + (days === 1 ? " day" : " days") : "—";
  }

  function formatPrice(minor, currency) {
    var unit = String(currency || "—").toUpperCase();
    var raw = String(minor == null ? "0" : minor);
    if (!/^-?\d+$/.test(raw)) return "—";
    var negative = raw.charAt(0) === "-";
    raw = negative ? raw.slice(1) : raw;
    var exponent = Object.prototype.hasOwnProperty.call(currencyExponents, unit) ? currencyExponents[unit] : 2;
    while (raw.length <= exponent) raw = "0" + raw;
    var whole = exponent ? raw.slice(0, -exponent) : raw;
    var fraction = exponent ? "." + raw.slice(-exponent) : "";
    whole = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
    return (negative ? "-" : "") + unit + " " + whole + fraction;
  }

  function statusClass(status) {
    return status === "ACTIVE" ? "green" : "gray";
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

  function appendPlanCell(row, plan) {
    var cell = document.createElement("td");
    var entity = document.createElement("div");
    var mark = document.createElement("span");
    var copy = document.createElement("span");
    var name = document.createElement("strong");
    var detail = document.createElement("small");
    var initials = String(plan.name || "?").trim().slice(0, 2).toUpperCase();

    entity.className = "entity";
    mark.className = "entity-mark";
    mark.textContent = initials;
    name.textContent = safeText(plan.name);
    detail.textContent = plan.description || (plan.max_devices + " devices · " + plan.max_concurrent_sessions + " sessions");
    copy.append(name, detail);
    entity.append(mark, copy);
    cell.appendChild(entity);
    row.appendChild(cell);
  }

  function setHeadings(table) {
    var headings = ["Plan", "Speed", "Price", "Duration", "Subscribers", "Status"];
    var headingRow = table.querySelector("thead tr");
    headingRow.replaceChildren();
    headings.forEach(function (value) {
      var heading = document.createElement("th");
      heading.textContent = value;
      headingRow.appendChild(heading);
    });
  }

  function displayPlans() {
    if (!loadedPlans || currentPage() !== "plans") return;
    var table = document.querySelector("#page-content .data-table");
    if (!table) return;

    setHeadings(table);
    var body = table.querySelector("tbody");
    body.replaceChildren();
    if (loadedPlans.length === 0) {
      var emptyRow = document.createElement("tr");
      var emptyCell = document.createElement("td");
      emptyCell.colSpan = 6;
      emptyCell.textContent = "No plans match this view.";
      emptyRow.appendChild(emptyCell);
      body.appendChild(emptyRow);
      return;
    }

    loadedPlans.forEach(function (plan) {
      var row = document.createElement("tr");
      appendPlanCell(row, plan);
      appendTextCell(row, formatRate(plan.download_bps) + " / " + formatRate(plan.upload_bps));
      appendTextCell(row, formatPrice(plan.price_minor, plan.currency));
      appendTextCell(row, formatDuration(plan.duration_seconds));
      appendTextCell(row, plan.active_subscriptions);
      appendStatusCell(row, plan.status);
      body.appendChild(row);
    });
  }

  function requestPlans() {
    if (loadedPlans || requestInFlight) return;
    requestInFlight = true;
    fetch(apiBase + "/api/v1/plans?limit=25", {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) return null;
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) return;
        loadedPlans = payload.data;
        displayPlans();
      })
      .catch(function () {
        // The authorised page remains empty when its API request fails.
      })
      .finally(function () {
        requestInFlight = false;
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "plans") return;
    if (loadedPlans) displayPlans();
    else requestPlans();
  }

  window.addEventListener("netcore:page-rendered", onPageRendered);
  if (currentPage() === "plans") onPageRendered({ detail: "plans" });
}());
