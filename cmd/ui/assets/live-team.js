(function () {
  "use strict";

  var loadedMembers = null;
  var requestInFlight = false;
  var apiBase = window.NETCORE_API_URL || "http://127.0.0.1:8080";

  function currentPage() {
    return window.location.hash.slice(1) || "overview";
  }

  function safeText(value) {
    return value == null || value === "" ? "—" : String(value);
  }

  function displayName(member) {
    return member.email || member.phone || "Unlabelled member";
  }

  function initials(member) {
    var value = displayName(member).replace(/[^a-z0-9]/gi, "");
    return value.slice(0, 2).toUpperCase() || "?";
  }

  function relativeTime(value) {
    if (!value) return "Never";
    var date = new Date(value);
    var seconds = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000));
    if (!Number.isFinite(seconds)) return "—";
    if (seconds < 60) return "Now";
    if (seconds < 3600) return Math.floor(seconds / 60) + " min ago";
    if (seconds < 86400) return Math.floor(seconds / 3600) + " h ago";
    return Math.floor(seconds / 86400) + " d ago";
  }

  function accountClass(status) {
    if (status === "ACTIVE") return "green";
    if (status === "LOCKED") return "amber";
    return "gray";
  }

  function mfaClass(status) {
    if (status === "ENABLED") return "green";
    if (status === "PENDING") return "amber";
    return "gray";
  }

  function label(value) {
    return safeText(value).toLowerCase().replace(/_/g, " ").replace(/\b\w/g, function (letter) { return letter.toUpperCase(); });
  }

  function appendTextCell(row, value) {
    var cell = document.createElement("td");
    cell.textContent = safeText(value);
    row.appendChild(cell);
  }

  function appendTagCell(row, value, className) {
    var cell = document.createElement("td");
    var tag = document.createElement("span");
    tag.className = "tag " + className;
    tag.textContent = label(value);
    cell.appendChild(tag);
    row.appendChild(cell);
  }

  function appendMemberCell(row, member) {
    var cell = document.createElement("td");
    var entity = document.createElement("div");
    var mark = document.createElement("span");
    var copy = document.createElement("span");
    var name = document.createElement("strong");
    var detail = document.createElement("small");

    entity.className = "entity";
    mark.className = "entity-mark";
    mark.textContent = initials(member);
    name.textContent = displayName(member);
    detail.textContent = member.email && member.phone ? member.phone : member.email ? "Email identity" : "Phone identity";
    copy.append(name, detail);
    entity.append(mark, copy);
    cell.appendChild(entity);
    row.appendChild(cell);
  }

  function setHeadings(table) {
    var headings = ["Team member", "Role", "Last active", "MFA", "Access"];
    var headingRow = table.querySelector("thead tr");
    headingRow.replaceChildren();
    headings.forEach(function (value) {
      var heading = document.createElement("th");
      heading.textContent = value;
      headingRow.appendChild(heading);
    });
  }

  function displayMembers() {
    if (!loadedMembers || currentPage() !== "team") return;
    var table = document.querySelector("#page-content .data-table");
    if (!table) return;

    setHeadings(table);
    var body = table.querySelector("tbody");
    body.replaceChildren();
    if (loadedMembers.length === 0) {
      var emptyRow = document.createElement("tr");
      var emptyCell = document.createElement("td");
      emptyCell.colSpan = 5;
      emptyCell.textContent = "No team members match this view.";
      emptyRow.appendChild(emptyCell);
      body.appendChild(emptyRow);
      return;
    }

    loadedMembers.forEach(function (member) {
      var row = document.createElement("tr");
      appendMemberCell(row, member);
      appendTextCell(row, Array.isArray(member.roles) && member.roles.length ? member.roles.join(", ") : "No role assigned");
      appendTextCell(row, relativeTime(member.last_seen_at));
      appendTagCell(row, member.mfa_status, mfaClass(member.mfa_status));
      appendTagCell(row, member.status, accountClass(member.status));
      body.appendChild(row);
    });
  }

  function requestMembers() {
    if (loadedMembers || requestInFlight) return;
    requestInFlight = true;
    fetch(apiBase + "/api/v1/team/members?limit=25", {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) return null;
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) return;
        loadedMembers = payload.data;
        displayMembers();
      })
      .catch(function () {
        // The unauthenticated or offline preview remains useful by design.
      })
      .finally(function () {
        requestInFlight = false;
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "team") return;
    if (loadedMembers) displayMembers();
    else requestMembers();
  }

  window.addEventListener("netcore:page-rendered", onPageRendered);
  if (currentPage() === "team") onPageRendered({ detail: "team" });
}());
