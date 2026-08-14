(function () {
  "use strict";

  var loadedWorkflows = null;
  var requestInFlight = false;
  var apiBase = window.NETCORE_API_URL || "http://127.0.0.1:8080";

  function currentPage() {
    return window.location.hash.slice(1) || "overview";
  }

  function safeText(value) {
    return value == null || value === "" ? "—" : String(value);
  }

  function label(value) {
    return safeText(value).toLowerCase().replace(/_/g, " ").replace(/\b\w/g, function (letter) {
      return letter.toUpperCase();
    });
  }

  function statusClass(status) {
    if (status === "READY") return "green";
    if (status === "DRAFT") return "amber";
    return "gray";
  }

  function relativeTime(value) {
    if (!value) return "Not scheduled";
    var date = new Date(value);
    var seconds = Math.floor((date.getTime() - Date.now()) / 1000);
    if (!Number.isFinite(seconds)) return "—";
    if (seconds <= 0) return "Due now";
    if (seconds < 60) return "In under a minute";
    if (seconds < 3600) return "In " + Math.floor(seconds / 60) + " min";
    if (seconds < 86400) return "In " + Math.floor(seconds / 3600) + " h";
    return "In " + Math.floor(seconds / 86400) + " d";
  }

  function appendTextCell(row, value) {
    var cell = document.createElement("td");
    cell.textContent = safeText(value);
    row.appendChild(cell);
  }

  function appendWorkflowCell(row, workflow) {
    var cell = document.createElement("td");
    var name = document.createElement("strong");
    var trigger = document.createElement("small");
    name.textContent = safeText(workflow.name);
    trigger.textContent = "Automation workflow";
    cell.append(name, trigger);
    row.appendChild(cell);
  }

  function appendStatusCell(row, status) {
    var cell = document.createElement("td");
    var tag = document.createElement("span");
    tag.className = "tag " + statusClass(status);
    tag.textContent = label(status);
    cell.appendChild(tag);
    row.appendChild(cell);
  }

  function setHeadings(table) {
    var headings = ["Workflow", "Trigger", "Next run", "Owner", "Status"];
    var headingRow = table.querySelector("thead tr");
    headingRow.replaceChildren();
    headings.forEach(function (value) {
      var heading = document.createElement("th");
      heading.textContent = value;
      headingRow.appendChild(heading);
    });
  }

  function displayWorkflows() {
    if (!loadedWorkflows || currentPage() !== "automations") return;
    var table = document.querySelector("#page-content .data-table");
    if (!table) return;
    setHeadings(table);
    var body = table.querySelector("tbody");
    body.replaceChildren();
    if (loadedWorkflows.length === 0) {
      var emptyRow = document.createElement("tr");
      var emptyCell = document.createElement("td");
      emptyCell.colSpan = 5;
      emptyCell.textContent = "No automation workflows match this view.";
      emptyRow.appendChild(emptyCell);
      body.appendChild(emptyRow);
      return;
    }
    loadedWorkflows.forEach(function (workflow) {
      var row = document.createElement("tr");
      appendWorkflowCell(row, workflow);
      appendTextCell(row, workflow.trigger_description);
      appendTextCell(row, relativeTime(workflow.next_run_at));
      appendTextCell(row, workflow.owner);
      appendStatusCell(row, workflow.status);
      body.appendChild(row);
    });
  }

  function requestWorkflows() {
    if (loadedWorkflows || requestInFlight) return;
    requestInFlight = true;
    fetch(apiBase + "/api/v1/automations?limit=25", {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) return null;
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) return;
        loadedWorkflows = payload.data;
        displayWorkflows();
      })
      .catch(function () {
        // The offline or unauthenticated preview remains useful by design.
      })
      .finally(function () {
        requestInFlight = false;
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "automations") return;
    if (loadedWorkflows) displayWorkflows();
    else requestWorkflows();
  }

  window.addEventListener("netcore:page-rendered", onPageRendered);
  if (currentPage() === "automations") onPageRendered({ detail: "automations" });
}());
