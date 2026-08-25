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
    showState: showState
  };
}());
