(function () {
  "use strict";

  var loadedRouters = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var livePage = window.NetCoreLivePage;

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

  function requestRouters(force) {
    if (requestInFlight || (loadedRouters && !force)) return;
    requestInFlight = true;
    if (!loadedRouters) showState("loading");
    fetch(apiBase + "/api/v1/network/routers?limit=25", {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) throw new Error("Network request failed");
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) throw new Error("Network response was invalid");
        loadedRouters = payload.data;
        displayRouters();
      })
      .catch(function () {
        // Last verified records remain visible when a refresh fails.
        if (loadedRouters) displayRouters();
        showState("error", { message: "Network data could not be loaded. Please try again.", preserve: Boolean(loadedRouters), retry: function () { requestRouters(true); } });
      })
      .finally(function () {
        requestInFlight = false;
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "network") return;
    if (loadedRouters) requestRouters(true);
    else requestRouters();
  }

  livePage.subscribe(function (page) { onPageRendered({ detail: page }); });
  if (currentPage() === "network") onPageRendered({ detail: "network" });
}());
