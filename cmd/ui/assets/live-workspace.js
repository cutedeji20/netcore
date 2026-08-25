(function () {
  "use strict";

  var workspace = null;
  var workspaceIsEmpty = false;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var livePage = window.NetCoreLivePage;

  function currentPage() {
    return livePage.current();
  }

  function showState(state, options) {
    livePage.showState("settings", state, options);
  }

  function safeText(value) {
    return value == null || value === "" ? "—" : String(value);
  }

  function label(value) {
    return safeText(value).toLowerCase().replace(/_/g, " ").replace(/\b\w/g, function (letter) {
      return letter.toUpperCase();
    });
  }

  function formatDate(value) {
    var date = new Date(value);
    if (!Number.isFinite(date.getTime())) return "—";
    return date.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
  }

  function appendRow(body, setting, value) {
    var row = document.createElement("tr");
    var name = document.createElement("td");
    var content = document.createElement("td");
    name.textContent = setting;
    content.textContent = safeText(value);
    row.append(name, content);
    body.appendChild(row);
  }

  function displayWorkspace() {
    if (currentPage() !== "settings") return;
    if (workspaceIsEmpty) {
      showState("empty", { message: "No workspace settings are available for this view." });
      return;
    }
    if (!workspace) return;
    var content = document.querySelector("#page-content");
    var table = content.querySelector(".data-table");
    if (!table) return;

    var headingRow = table.querySelector("thead tr");
    headingRow.replaceChildren();
    ["Setting", "Current value"].forEach(function (value) {
      var heading = document.createElement("th");
      heading.textContent = value;
      headingRow.appendChild(heading);
    });

    var body = table.querySelector("tbody");
    body.replaceChildren();
    appendRow(body, "Workspace name", workspace.name);
    appendRow(body, "Workspace status", label(workspace.status));
    appendRow(body, "Timezone", workspace.timezone);
    appendRow(body, "Currency", workspace.currency);
    appendRow(body, "Registered routers", workspace.registered_routers);
    appendRow(body, "Active team members", workspace.active_team_members);
    appendRow(body, "Profile last updated", formatDate(workspace.updated_at));

    var metricNames = ["Workspace status", "Registered routers", "Active team members", "Currency"];
    var metricValues = [label(workspace.status), workspace.registered_routers, workspace.active_team_members, workspace.currency];
    content.querySelectorAll(".metric").forEach(function (metric, index) {
      var name = metric.querySelector(".metric-label");
      var value = metric.querySelector(".metric-value");
      if (name && metricNames[index]) name.textContent = metricNames[index];
      if (value && metricValues[index] != null) value.textContent = metricValues[index];
    });

    var side = content.querySelector(".detail-list");
    if (side) {
      side.replaceChildren();
      [["Workspace", workspace.name], ["Login workspace", workspace.slug], ["Timezone", workspace.timezone], ["Currency", workspace.currency]].forEach(function (item) {
        var entry = document.createElement("li");
        var name = document.createElement("span");
        var value = document.createElement("strong");
        name.textContent = item[0];
        value.textContent = safeText(item[1]);
        entry.append(name, value);
        side.appendChild(entry);
      });
    }
    showState("records");
  }

  function requestWorkspace(force) {
    if (requestInFlight || ((workspace || workspaceIsEmpty) && !force)) return;
    requestInFlight = true;
    if (!workspace) showState("loading");
    fetch(apiBase + "/api/v1/workspace/settings", {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) throw new Error("Workspace request failed");
        return response.json();
      })
      .then(function (payload) {
        if (payload && Object.keys(payload).length === 0) {
          workspaceIsEmpty = true;
          displayWorkspace();
          return;
        }
        if (!payload || typeof payload.name !== "string") throw new Error("Workspace response was invalid");
        workspace = payload;
        workspaceIsEmpty = false;
        displayWorkspace();
      })
      .catch(function () {
        // Last verified records remain visible when a refresh fails.
        if (workspace || workspaceIsEmpty) displayWorkspace();
        showState("error", { message: "Workspace settings could not be loaded. Please try again.", preserve: Boolean(workspace || workspaceIsEmpty), retry: function () { requestWorkspace(true); } });
      })
      .finally(function () {
        requestInFlight = false;
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "settings") return;
    if (workspace || workspaceIsEmpty) requestWorkspace(true);
    else requestWorkspace();
  }

  livePage.subscribe(function (page) { onPageRendered({ detail: page }); });
  if (currentPage() === "settings") onPageRendered({ detail: "settings" });
}());
