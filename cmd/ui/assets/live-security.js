(function () {
  "use strict";

  var loadedEvents = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var livePage = window.NetCoreLivePage;

  function currentPage() {
    return livePage.current();
  }

  function showState(state, options) {
    livePage.showState("security", state, options);
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
      showState("empty", { message: "No recorded security activity matches this view." });
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
    showState("records");
  }

  function requestEvents(force) {
    if (requestInFlight || (loadedEvents && !force)) return;
    requestInFlight = true;
    if (!loadedEvents) showState("loading");
    fetch(apiBase + "/api/v1/security/events?limit=25", {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) throw new Error("Security events request failed");
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) throw new Error("Security events response was invalid");
        loadedEvents = payload.data;
        displayEvents();
      })
      .catch(function () {
        // Last verified records remain visible when a refresh fails.
        if (loadedEvents) displayEvents();
        showState("error", { message: "Security activity could not be loaded. Please try again.", preserve: Boolean(loadedEvents), retry: function () { requestEvents(true); } });
      })
      .finally(function () {
        requestInFlight = false;
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "security") return;
    if (loadedEvents) requestEvents(true);
    else requestEvents();
  }

  livePage.subscribe(function (page) { onPageRendered({ detail: page }); });
  if (currentPage() === "security") onPageRendered({ detail: "security" });
}());
