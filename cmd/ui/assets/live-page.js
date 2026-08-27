(function () {
  "use strict";

  var active = window.location.hash.slice(1) || "overview";
  var subscribers = [];

  function renderedPage(event) {
    active = event.detail || "overview";
    subscribers.slice().forEach(function (subscriber) { subscriber(active); });
  }

  function liveTable(page) {
    if (active !== page) return null;
    return document.querySelector("#page-content .data-table");
  }

  function listMount(page) {
    if (active !== page) return null;
    return document.querySelector('[data-live-list-controls="' + page + '"]');
  }

  function renderListControls(page, options) {
    options = options || {};
    var mount = listMount(page);
    if (!mount) return null;
    var pagination = document.querySelector('[data-live-list-pagination="' + page + '"]');
    mount.replaceChildren();
    if (pagination) pagination.replaceChildren();
    var search = document.createElement("input");
    search.type = "search";
    search.value = options.query || "";
    search.placeholder = options.searchPlaceholder || "Search records";
    search.setAttribute("aria-label", options.searchLabel || "Search records");
    search.disabled = !!options.busy;
    search.addEventListener("input", function () { if (!options.busy && typeof options.onSearch === "function") options.onSearch(search.value); });
    mount.appendChild(search);
    if (Array.isArray(options.filters) && options.filters.length) {
      var select = document.createElement("select");
      select.setAttribute("aria-label", options.filterLabel || "Filter records");
      select.disabled = !!options.busy;
      options.filters.forEach(function (filter) {
        var option = document.createElement("option");
        option.value = filter.value == null ? "" : String(filter.value);
        option.textContent = filter.label == null ? option.value : String(filter.label);
        option.selected = option.value === String(options.filter || "");
        select.appendChild(option);
      });
      select.addEventListener("change", function () { if (!options.busy && typeof options.onFilter === "function") options.onFilter(select.value); });
      mount.appendChild(select);
    }
    if (!pagination) return mount;
    var previous = document.createElement("button");
    previous.type = "button";
    previous.className = "button";
    previous.textContent = "Previous";
    previous.disabled = !!options.busy || !options.hasPrevious;
    previous.addEventListener("click", function () { if (!options.busy && typeof options.onPrevious === "function") options.onPrevious(); });
    var next = document.createElement("button");
    next.type = "button";
    next.className = "button";
    next.textContent = "Next";
    next.disabled = !!options.busy || !options.hasNext;
    next.addEventListener("click", function () { if (!options.busy && typeof options.onNext === "function") options.onNext(); });
    pagination.append(previous, next);
    return mount;
  }

  function liveState(table) {
    var state = table.parentNode.querySelector(".live-data-state");
    if (state) return state;
    state = document.createElement("p");
    state.className = "live-data-state description";
    state.setAttribute("role", "status");
    table.parentNode.insertBefore(state, table);
    return state;
  }

  function replaceRows(table, message) {
    var body = table.querySelector("tbody");
    if (!body) return;
    var row = document.createElement("tr");
    var cell = document.createElement("td");
    cell.colSpan = Math.max(1, table.querySelectorAll("thead th").length);
    cell.className = "empty-cell";
    cell.textContent = message;
    row.appendChild(cell);
    body.replaceChildren(row);
  }

  function showState(page, state, options) {
    options = options || {};
    var table = liveTable(page);
    if (!table) return;
    var status = liveState(table);
    table.setAttribute("data-live-state", state);
    status.replaceChildren();

    if (state === "records") {
      status.remove();
      return;
    }

    var message = options.message || (state === "loading"
      ? "Loading authorised live data…"
      : state === "empty"
        ? "No verified records are available for this view."
        : "This data could not be loaded. Please try again.");
    status.appendChild(document.createTextNode(message));

    if (state === "error" && typeof options.retry === "function") {
      var retry = document.createElement("button");
      retry.type = "button";
      retry.className = "button";
      retry.textContent = "Retry";
      retry.addEventListener("click", options.retry);
      status.appendChild(document.createTextNode(" "));
      status.appendChild(retry);
    }

    if (!options.preserve) replaceRows(table, message);
  }

  window.addEventListener("netcore:page-rendered", renderedPage);
  window.NetCoreLivePage = {
    current: function () { return active; },
    subscribe: function (fn) {
      if (typeof fn === "function") subscribers.push(fn);
    },
    showState: showState,
    listMount: listMount,
    renderListControls: renderListControls
  };
}());
