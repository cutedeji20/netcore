(function () {
  "use strict";

  var loadedEvents = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var livePage = window.NetCoreLivePage;
  var listConfig = window.NetCoreLiveListConfig.get("security");
  var listState = window.NetCoreLiveListControls.createState(listConfig.filters, listConfig.initialFilter, listConfig.filterParam);
  var loadedEventsMeta = {};
  var searchTimer = 0;
  var pendingQuery = "";
  var criteriaPending = false;
  var requestVersion = 0;

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

  function renderControls() {
    if (currentPage() !== "security") return;
    livePage.renderListControls("security", {
      query: criteriaPending ? pendingQuery : listState.query, filter: listState.filter, filters: [], busy: requestInFlight,
      hasPrevious: listState.previousCursors.length > 0, hasNext: listState.hasMore,
      searchPlaceholder: "Search security activity", searchLabel: "Search security activity",
      onSearch: function (query) {
        clearTimeout(searchTimer);
        pendingQuery = query;
        criteriaPending = true;
        requestVersion += 1;
        window.NetCoreLiveListControls.applyCriteria(listState, query, listState.filter);
        searchTimer = setTimeout(function () { criteriaPending = false; requestVersion += 1; requestEvents(true); }, 250);
      },
      onNext: function () { if (criteriaPending) return; if (window.NetCoreLiveListControls.nextPage(listState)) requestEvents(true); else renderControls(); },
      onPrevious: function () { if (criteriaPending) return; if (listState.previousCursors.length) { window.NetCoreLiveListControls.previousPage(listState); requestEvents(true); } else renderControls(); }
    });
  }

  function applyCriteria(query) {
    if (window.NetCoreLiveListControls.applyCriteria(listState, query, listState.filter)) requestEvents(true);
  }

  function requestEvents(force) {
    if (requestInFlight || (loadedEvents && !force)) return;
    requestInFlight = true;
    var requestVersionAtStart = requestVersion;
    renderControls();
    if (!loadedEvents) showState("loading");
    fetch(window.NetCoreLiveListControls.requestURL(apiBase, listConfig.endpoint, listState, 25), {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) throw new Error("Security events request failed");
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) throw new Error("Security events response was invalid");
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        loadedEvents = payload.data;
        loadedEventsMeta = payload.meta || {};
        window.NetCoreLiveListControls.applyResponseMeta(listState, loadedEventsMeta);
        displayEvents();
      })
      .catch(function () {
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        // Last verified records remain visible when a refresh fails.
        if (loadedEvents) displayEvents();
        showState("error", { message: "Security activity could not be loaded. Please try again.", preserve: Boolean(loadedEvents), retry: function () { requestEvents(true); } });
      })
      .finally(function () {
        requestInFlight = false;
        renderControls();
        if (!criteriaPending && requestVersionAtStart !== requestVersion) requestEvents(true);
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "security") return;
    renderControls();
    if (loadedEvents) requestEvents(true);
    else requestEvents();
  }

  livePage.subscribe(function (page) { onPageRendered({ detail: page }); });
}());
