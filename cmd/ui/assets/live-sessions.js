(function () {
  "use strict";

  var loadedSessions = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var livePage = window.NetCoreLivePage;
  var listConfig = window.NetCoreLiveListConfig.get("sessions");
  var listState = window.NetCoreLiveListControls.createState(listConfig.filters, listConfig.initialFilter, listConfig.filterParam);
  var loadedSessionsMeta = {};
  var searchTimer = 0;
  var pendingQuery = "";
  var criteriaPending = false;
  var requestVersion = 0;

  function currentPage() {
    return livePage.current();
  }

  function showState(state, options) {
    livePage.showState("sessions", state, options);
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
      showState("empty", { message: "No sessions match this view." });
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
    showState("records");
  }

  function filterOptions() {
    return listConfig.filters.map(function (filter) { return { value: filter, label: filter || "All" }; });
  }

  function renderControls() {
    if (currentPage() !== "sessions") return;
    livePage.renderListControls("sessions", {
      query: criteriaPending ? pendingQuery : listState.query, filter: listState.filter, filters: filterOptions(), busy: requestInFlight,
      hasPrevious: listState.previousCursors.length > 0, hasNext: listState.hasMore,
      searchPlaceholder: "Search sessions", searchLabel: "Search sessions", filterLabel: "Filter sessions",
      onSearch: function (query) {
        clearTimeout(searchTimer);
        pendingQuery = query;
        criteriaPending = true;
        requestVersion += 1;
        window.NetCoreLiveListControls.applyCriteria(listState, query, listState.filter);
        searchTimer = setTimeout(function () { criteriaPending = false; requestVersion += 1; requestSessions(true); }, 250);
      },
      onFilter: function (filter) { applyCriteria(listState.query, filter); },
      onNext: function () { if (criteriaPending) return; if (window.NetCoreLiveListControls.nextPage(listState)) requestSessions(true); else renderControls(); },
      onPrevious: function () { if (criteriaPending) return; if (listState.previousCursors.length) { window.NetCoreLiveListControls.previousPage(listState); requestSessions(true); } else renderControls(); }
    });
  }

  function applyCriteria(query, filter) {
    if (window.NetCoreLiveListControls.applyCriteria(listState, query, filter)) requestSessions(true);
  }

  function requestSessions(force) {
    if (requestInFlight || (loadedSessions && !force)) return;
    requestInFlight = true;
    var requestVersionAtStart = requestVersion;
    renderControls();
    if (!loadedSessions) showState("loading");
    fetch(window.NetCoreLiveListControls.requestURL(apiBase, listConfig.endpoint, listState, 25), {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) throw new Error("Sessions request failed");
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) throw new Error("Sessions response was invalid");
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        loadedSessions = payload.data;
        loadedSessionsMeta = payload.meta || {};
        window.NetCoreLiveListControls.applyResponseMeta(listState, loadedSessionsMeta);
        displaySessions();
      })
      .catch(function () {
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        // Last verified records remain visible when a refresh fails.
        if (loadedSessions) displaySessions();
        showState("error", { message: "Sessions could not be loaded. Please try again.", preserve: Boolean(loadedSessions), retry: function () { requestSessions(true); } });
      })
      .finally(function () {
        requestInFlight = false;
        renderControls();
        if (!criteriaPending && requestVersionAtStart !== requestVersion) requestSessions(true);
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "sessions") return;
    renderControls();
    if (loadedSessions) requestSessions(true);
    else requestSessions();
  }

  livePage.subscribe(function (page) { onPageRendered({ detail: page }); });
}());
