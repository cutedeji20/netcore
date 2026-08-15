(function () {
  "use strict";

  var loadedEvents = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");

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

  function relativeTime(value) {
    var date = new Date(value);
    var seconds = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000));
    if (!Number.isFinite(seconds)) return "—";
    if (seconds < 60) return "Now";
    if (seconds < 3600) return Math.floor(seconds / 60) + " min ago";
    if (seconds < 86400) return Math.floor(seconds / 3600) + " h ago";
    return Math.floor(seconds / 86400) + " d ago";
  }

  function appendTextCell(row, value) {
    var cell = document.createElement("td");
    cell.textContent = safeText(value);
    row.appendChild(cell);
  }

  function appendEventCell(row, event) {
    var cell = document.createElement("td");
    var eventName = document.createElement("strong");
    var hint = document.createElement("small");
    eventName.textContent = label(event.action);
    hint.textContent = "Recorded audit activity";
    cell.append(eventName, hint);
    row.appendChild(cell);
  }

  function setHeadings(table) {
    var headings = ["Time", "Activity", "Actor", "Resource"];
    var headingRow = table.querySelector("thead tr");
    headingRow.replaceChildren();
    headings.forEach(function (value) {
      var heading = document.createElement("th");
      heading.textContent = value;
      headingRow.appendChild(heading);
    });
  }

  function displayEvents() {
    if (!loadedEvents || currentPage() !== "security") return;
    var table = document.querySelector("#page-content .data-table");
    if (!table) return;

    setHeadings(table);
    var body = table.querySelector("tbody");
    body.replaceChildren();
    if (loadedEvents.length === 0) {
      var emptyRow = document.createElement("tr");
      var emptyCell = document.createElement("td");
      emptyCell.colSpan = 4;
      emptyCell.textContent = "No recorded security activity matches this view.";
      emptyRow.appendChild(emptyCell);
      body.appendChild(emptyRow);
      return;
    }

    loadedEvents.forEach(function (event) {
      var row = document.createElement("tr");
      appendTextCell(row, relativeTime(event.created_at));
      appendEventCell(row, event);
      appendTextCell(row, event.actor);
      appendTextCell(row, event.resource_type);
      body.appendChild(row);
    });
  }

  function requestEvents() {
    if (loadedEvents || requestInFlight) return;
    requestInFlight = true;
    fetch(apiBase + "/api/v1/security/events?limit=25", {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) return null;
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) return;
        loadedEvents = payload.data;
        displayEvents();
      })
      .catch(function () {
        // The authorised page remains empty when its API request fails.
      })
      .finally(function () {
        requestInFlight = false;
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "security") return;
    if (loadedEvents) displayEvents();
    else requestEvents();
  }

  window.addEventListener("netcore:page-rendered", onPageRendered);
  if (currentPage() === "security") onPageRendered({ detail: "security" });
}());
