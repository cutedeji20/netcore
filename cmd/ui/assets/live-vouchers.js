(function () {
  "use strict";

  var loadedBatches = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");

  function currentPage() {
    return window.location.hash.slice(1) || "overview";
  }

  function safeText(value) {
    return value == null || value === "" ? "—" : String(value);
  }

  function formatDate(value) {
    if (!value) return "No expiry";
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) return "—";
    return "Expires " + date.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
  }

  function statusClass(status) {
    if (status === "ACTIVE") return "green";
    if (status === "MIXED") return "amber";
    if (status === "EXPIRED") return "amber";
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

  function appendBatchCell(row, batch) {
    var cell = document.createElement("td");
    var entity = document.createElement("div");
    var mark = document.createElement("span");
    var copy = document.createElement("span");
    var name = document.createElement("strong");
    var detail = document.createElement("small");

    entity.className = "entity";
    mark.className = "entity-mark";
    mark.textContent = "VC";
    name.textContent = "Batch " + safeText(String(batch.id || "").slice(0, 8));
    detail.textContent = formatDate(batch.expires_at);
    copy.append(name, detail);
    entity.append(mark, copy);
    cell.appendChild(entity);
    row.appendChild(cell);
  }

  function appendPlanCell(row, batch) {
    var cell = document.createElement("td");
    var entity = document.createElement("div");
    var copy = document.createElement("span");
    var name = document.createElement("strong");
    var detail = document.createElement("small");

    entity.className = "entity";
    name.textContent = safeText(batch.plan_name);
    detail.textContent = safeText(batch.available) + " available";
    copy.append(name, detail);
    entity.append(copy);
    cell.appendChild(entity);
    row.appendChild(cell);
  }

  function setHeadings(table) {
    var headings = ["Batch", "Access bundle", "Issued", "Redeemed", "Status"];
    var headingRow = table.querySelector("thead tr");
    headingRow.replaceChildren();
    headings.forEach(function (value) {
      var heading = document.createElement("th");
      heading.textContent = value;
      headingRow.appendChild(heading);
    });
  }

  function displayBatches() {
    if (!loadedBatches || currentPage() !== "vouchers") return;
    var table = document.querySelector("#page-content .data-table");
    if (!table) return;

    setHeadings(table);
    var body = table.querySelector("tbody");
    body.replaceChildren();
    if (loadedBatches.length === 0) {
      var emptyRow = document.createElement("tr");
      var emptyCell = document.createElement("td");
      emptyCell.colSpan = 5;
      emptyCell.textContent = "No voucher batches match this view.";
      emptyRow.appendChild(emptyCell);
      body.appendChild(emptyRow);
      return;
    }

    loadedBatches.forEach(function (batch) {
      var row = document.createElement("tr");
      appendBatchCell(row, batch);
      appendPlanCell(row, batch);
      appendTextCell(row, batch.issued);
      appendTextCell(row, batch.redeemed);
      appendStatusCell(row, batch.status);
      body.appendChild(row);
    });
  }

  function requestBatches() {
    if (loadedBatches || requestInFlight) return;
    requestInFlight = true;
    fetch(apiBase + "/api/v1/vouchers/batches?limit=25", {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) return null;
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) return;
        loadedBatches = payload.data;
        displayBatches();
      })
      .catch(function () {
        // The authorised page remains empty when its API request fails.
      })
      .finally(function () {
        requestInFlight = false;
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "vouchers") return;
    if (loadedBatches) displayBatches();
    else requestBatches();
  }

  window.addEventListener("netcore:page-rendered", onPageRendered);
  if (currentPage() === "vouchers") onPageRendered({ detail: "vouchers" });
}());
