(function () {
  "use strict";

  var loadedRouters = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var livePage = window.NetCoreLivePage;
  var listConfig = window.NetCoreLiveListConfig.get("network");
  var listState = window.NetCoreLiveListControls.createState(listConfig.filters, listConfig.initialFilter, listConfig.filterParam);
  var loadedRoutersMeta = {};
  var searchTimer = 0;
  var pendingQuery = "";
  var criteriaPending = false;
  var requestVersion = 0;

  function currentPage() {
    return livePage.current();
  }

  function showState(state, options) {
    livePage.showState("network", state, options);
  }

  function safeText(value) {
    return value == null || value === "" ? "—" : String(value);
  }

  function relativeTime(value) {
    if (!value) return "Never";
    var date = new Date(value);
    var seconds = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000));
    if (!Number.isFinite(seconds)) return "—";
    if (seconds < 60) return "Just now";
    if (seconds < 3600) return Math.floor(seconds / 60) + " min ago";
    if (seconds < 86400) return Math.floor(seconds / 3600) + " h ago";
    return Math.floor(seconds / 86400) + " d ago";
  }

  function routerStatusClass(status) {
    if (status === "ONLINE") return "green";
    if (status === "PROVISIONING") return "amber";
    if (status === "OFFLINE") return "red";
    return "gray";
  }

  function routerStatusLabel(status) {
    return safeText(status).toLowerCase().replace(/\b\w/g, function (letter) { return letter.toUpperCase(); });
  }

  function aaaStatusClass(status) {
    return status === "ACTIVE" ? "green" : status === "DISABLED" ? "amber" : "gray";
  }

  function aaaStatusLabel(status) {
    if (status === "NOT_CONFIGURED") return "Not configured";
    return safeText(status).toLowerCase().replace(/\b\w/g, function (letter) { return letter.toUpperCase(); });
  }

  function appendTextCell(row, value) {
    var cell = document.createElement("td");
    cell.textContent = safeText(value);
    row.appendChild(cell);
  }

  function appendTagCell(row, labelText, className) {
    var cell = document.createElement("td");
    var label = document.createElement("span");
    label.className = "tag " + className;
    label.textContent = labelText;
    cell.appendChild(label);
    row.appendChild(cell);
  }

  function appendLocationCell(row, router) {
    var cell = document.createElement("td");
    var entity = document.createElement("div");
    var mark = document.createElement("span");
    var copy = document.createElement("span");
    var name = document.createElement("strong");
    var detail = document.createElement("small");

    entity.className = "entity";
    mark.className = "entity-mark";
    mark.textContent = String(router.site_name || "?").trim().slice(0, 2).toUpperCase();
    name.textContent = safeText(router.site_name);
    detail.textContent = "Managed location";
    copy.append(name, detail);
    entity.append(mark, copy);
    cell.appendChild(entity);
    row.appendChild(cell);
  }

  function setHeadings(table) {
    var headings = ["Location", "Router", "AAA", "Last seen", "Status"];
    var headingRow = table.querySelector("thead tr");
    headingRow.replaceChildren();
    headings.forEach(function (value) {
      var heading = document.createElement("th");
      heading.textContent = value;
      headingRow.appendChild(heading);
    });
  }

  function displayRouters() {
    if (!loadedRouters || currentPage() !== "network") return;
    var table = document.querySelector("#page-content .data-table");
    if (!table) return;

    setHeadings(table);
    var body = table.querySelector("tbody");
    body.replaceChildren();
    if (loadedRouters.length === 0) {
      showState("empty", { message: "No routers match this view." });
      return;
    }

    loadedRouters.forEach(function (router) {
      var row = document.createElement("tr");
      appendLocationCell(row, router);
      appendTextCell(row, router.name);
      appendTagCell(row, aaaStatusLabel(router.aaa_status), aaaStatusClass(router.aaa_status));
      appendTextCell(row, relativeTime(router.last_seen_at));
      appendTagCell(row, routerStatusLabel(router.status), routerStatusClass(router.status));
      body.appendChild(row);
    });
    showState("records");
  }

  function filterOptions() {
    return listConfig.filters.map(function (filter) { return { value: filter, label: filter || "All" }; });
  }

  function renderControls() {
    if (currentPage() !== "network") return;
    livePage.renderListControls("network", {
      query: criteriaPending ? pendingQuery : listState.query, filter: listState.filter, filters: filterOptions(), busy: requestInFlight,
      hasPrevious: listState.previousCursors.length > 0, hasNext: listState.hasMore,
      searchPlaceholder: "Search routers", searchLabel: "Search routers", filterLabel: "Filter routers",
      onSearch: function (query) {
        clearTimeout(searchTimer);
        pendingQuery = query;
        criteriaPending = true;
        requestVersion += 1;
        window.NetCoreLiveListControls.applyCriteria(listState, query, listState.filter);
        searchTimer = setTimeout(function () { criteriaPending = false; requestVersion += 1; requestRouters(true); }, 250);
      },
      onFilter: function (filter) { applyCriteria(listState.query, filter); },
      onNext: function () { if (criteriaPending) return; if (window.NetCoreLiveListControls.nextPage(listState)) requestRouters(true); else renderControls(); },
      onPrevious: function () { if (criteriaPending) return; if (listState.previousCursors.length) { window.NetCoreLiveListControls.previousPage(listState); requestRouters(true); } else renderControls(); }
    });
  }

  function applyCriteria(query, filter) {
    if (window.NetCoreLiveListControls.applyCriteria(listState, query, filter)) requestRouters(true);
  }

  function requestRouters(force) {
    if (requestInFlight || (loadedRouters && !force)) return;
    requestInFlight = true;
    var requestVersionAtStart = requestVersion;
    renderControls();
    if (!loadedRouters) showState("loading");
    fetch(window.NetCoreLiveListControls.requestURL(apiBase, listConfig.endpoint, listState, 25), {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) throw new Error("Network request failed");
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) throw new Error("Network response was invalid");
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        loadedRouters = payload.data;
        loadedRoutersMeta = payload.meta || {};
        window.NetCoreLiveListControls.applyResponseMeta(listState, loadedRoutersMeta);
        displayRouters();
      })
      .catch(function () {
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        // Last verified records remain visible when a refresh fails.
        if (loadedRouters) displayRouters();
        showState("error", { message: "Network data could not be loaded. Please try again.", preserve: Boolean(loadedRouters), retry: function () { requestRouters(true); } });
      })
      .finally(function () {
        requestInFlight = false;
        renderControls();
        if (!criteriaPending && requestVersionAtStart !== requestVersion) requestRouters(true);
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "network") return;
    renderControls();
    if (loadedRouters) requestRouters(true);
    else requestRouters();
  }

  livePage.subscribe(function (page) { onPageRendered({ detail: page }); });
}());
