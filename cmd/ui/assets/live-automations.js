(function () {
  "use strict";

  var loadedWorkflows = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var livePage = window.NetCoreLivePage;
  var listConfig = window.NetCoreLiveListConfig.get("automations");
  var listState = window.NetCoreLiveListControls.createState(listConfig.filters, listConfig.initialFilter, listConfig.filterParam);
  var loadedWorkflowsMeta = {};
  var searchTimer = 0;
  var pendingQuery = "";
  var criteriaPending = false;
  var requestVersion = 0;

  function currentPage() {
    return livePage.current();
  }

  function showState(state, options) {
    livePage.showState("automations", state, options);
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
      showState("empty", { message: "No automation workflows match this view." });
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
    showState("records");
  }

  function filterOptions() {
    return listConfig.filters.map(function (filter) { return { value: filter, label: filter || "All" }; });
  }

  function renderControls() {
    if (currentPage() !== "automations") return;
    livePage.renderListControls("automations", {
      query: criteriaPending ? pendingQuery : listState.query, filter: listState.filter, filters: filterOptions(), busy: requestInFlight,
      hasPrevious: listState.previousCursors.length > 0, hasNext: listState.hasMore,
      searchPlaceholder: "Search automations", searchLabel: "Search automations", filterLabel: "Filter automations",
      onSearch: function (query) {
        clearTimeout(searchTimer);
        pendingQuery = query;
        criteriaPending = true;
        requestVersion += 1;
        window.NetCoreLiveListControls.applyCriteria(listState, query, listState.filter);
        searchTimer = setTimeout(function () { criteriaPending = false; requestVersion += 1; requestWorkflows(true); }, 250);
      },
      onFilter: function (filter) { applyCriteria(listState.query, filter); },
      onNext: function () { if (criteriaPending) return; if (window.NetCoreLiveListControls.nextPage(listState)) requestWorkflows(true); else renderControls(); },
      onPrevious: function () { if (criteriaPending) return; if (listState.previousCursors.length) { window.NetCoreLiveListControls.previousPage(listState); requestWorkflows(true); } else renderControls(); }
    });
  }

  function applyCriteria(query, filter) {
    if (window.NetCoreLiveListControls.applyCriteria(listState, query, filter)) requestWorkflows(true);
  }

  function requestWorkflows(force) {
    if (requestInFlight || (loadedWorkflows && !force)) return;
    requestInFlight = true;
    var requestVersionAtStart = requestVersion;
    renderControls();
    if (!loadedWorkflows) showState("loading");
    fetch(window.NetCoreLiveListControls.requestURL(apiBase, listConfig.endpoint, listState, 25), {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) throw new Error("Automations request failed");
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) throw new Error("Automations response was invalid");
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        loadedWorkflows = payload.data;
        loadedWorkflowsMeta = payload.meta || {};
        window.NetCoreLiveListControls.applyResponseMeta(listState, loadedWorkflowsMeta);
        displayWorkflows();
      })
      .catch(function () {
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        // Last verified records remain visible when a refresh fails.
        if (loadedWorkflows) displayWorkflows();
        showState("error", { message: "Automations could not be loaded. Please try again.", preserve: Boolean(loadedWorkflows), retry: function () { requestWorkflows(true); } });
      })
      .finally(function () {
        requestInFlight = false;
        renderControls();
        if (!criteriaPending && requestVersionAtStart !== requestVersion) requestWorkflows(true);
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "automations") return;
    renderControls();
    if (loadedWorkflows) requestWorkflows(true);
    else requestWorkflows();
  }

  livePage.subscribe(function (page) { onPageRendered({ detail: page }); });
}());
