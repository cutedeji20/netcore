function renderTeamActions(principal) {
  var permissions = principal && Array.isArray(principal.permissions) ? principal.permissions : [];
  if (permissions.indexOf("team.write") === -1) return "";
  return '<button class="button primary team-invite" type="button">Invite teammate</button>';
}
function teamErrorMessage(payload) {
  return payload && payload.error && typeof payload.error.message === "string" ? payload.error.message : "The team change could not be completed. Please try again.";
}
function reconcileInvitation(invitations, replacement, removedID) {
  var next = (invitations || []).filter(function (invitation) { return invitation.id !== removedID; });
  return replacement ? next.concat([replacement]) : next;
}
function teamMutationRequest(path, method, body) {
  return { url: path, method: method, credentials: "same-origin", cache: "no-store", headers: { "Content-Type": "application/json", "Accept": "application/json" }, body: JSON.stringify(body || {}) };
}
function teamStepUpFields() {
  return [
    { label: "Current password", name: "password", type: "password", maxLength: 1024 },
    { label: "Authenticator code", name: "mfa_code", type: "text", pattern: "[0-9]{6}", maxLength: 6 }
  ];
}

if (typeof module !== "undefined" && module.exports) module.exports = { renderTeamActions: renderTeamActions, teamErrorMessage: teamErrorMessage, reconcileInvitation: reconcileInvitation, teamMutationRequest: teamMutationRequest, teamStepUpFields: teamStepUpFields };

if (typeof window !== "undefined") (function () {
  "use strict";

  var loadedMembers = null;
  var requestInFlight = false;
  var invitationRequestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var livePage = window.NetCoreLivePage;
  var listConfig = window.NetCoreLiveListConfig.get("team");
  var listState = window.NetCoreLiveListControls.createState(listConfig.filters, listConfig.initialFilter, listConfig.filterParam);
  var roleFilter = "";
  var teamRoles = ["", "Administrator", "Operations", "Billing", "Support", "UNASSIGNED"];
  var loadedMembersMeta = {};
  var searchTimer = 0;
  var pendingQuery = "";
  var criteriaPending = false;
  var requestVersion = 0;
  var issuedInvitations = [];
  var selectedMemberIDs = new Set();

  function canWrite() {
    var principal = window.NETCORE_PRINCIPAL || {};
    return Array.isArray(principal.permissions) && principal.permissions.indexOf("team.write") !== -1;
  }

  function safeError(response) {
    return response.json().then(function (payload) {
      return teamErrorMessage(payload);
    }).catch(function () { return teamErrorMessage(null); });
  }

  function postJSON(path, method, body) {
    var request = teamMutationRequest(path, method, body);
    return fetch(apiBase + request.url, request);
  }

  function closeDialog(dialog) { dialog.remove(); }

  function sensitiveDialog(title, fields, submitLabel, onSubmit, noteText) {
    var backdrop = document.createElement("div");
    var form = document.createElement("form");
    var heading = document.createElement("h2");
    var note = document.createElement("p");
    var feedback = document.createElement("p");
    var footer = document.createElement("footer");
    var cancel = document.createElement("button");
    var submit = document.createElement("button");
    backdrop.className = "team-dialog-backdrop";
    form.className = "team-dialog";
    heading.textContent = title;
    note.textContent = noteText || "Confirm this sensitive action with your current password and six-digit authenticator code.";
    feedback.className = "team-form-feedback";
    fields.forEach(function (field) {
      var label = document.createElement("label");
      var input = document.createElement(field.options ? "select" : "input");
      label.className = "team-field";
      label.appendChild(document.createTextNode(field.label));
      input.name = field.name;
      input.required = field.required !== false;
      if (field.options) field.options.forEach(function (option) { var choice = document.createElement("option"); choice.value = option; choice.textContent = option; input.appendChild(choice); });
      else { input.type = field.type || "text"; if (field.pattern) input.pattern = field.pattern; if (field.minLength) input.minLength = field.minLength; if (field.maxLength) input.maxLength = field.maxLength; }
      label.appendChild(input); form.appendChild(label);
    });
    teamStepUpFields().forEach(function (field) {
      var label = document.createElement("label"); var input = document.createElement("input");
      label.className = "team-field"; label.appendChild(document.createTextNode(field.label));
      input.name = field.name; input.type = field.type; input.required = true; input.autocomplete = field.name === "password" ? "current-password" : "one-time-code"; input.inputMode = field.name === "mfa_code" ? "numeric" : "text"; if (field.pattern) input.pattern = field.pattern; input.maxLength = field.maxLength; label.appendChild(input); form.appendChild(label);
    });
    cancel.type = "button"; cancel.className = "button"; cancel.textContent = "Cancel"; cancel.addEventListener("click", function () { closeDialog(backdrop); });
    submit.type = "submit"; submit.className = "button primary"; submit.textContent = submitLabel;
    footer.append(cancel, submit); form.append(heading, note, feedback, footer); backdrop.appendChild(form); document.body.appendChild(backdrop);
    form.addEventListener("submit", function (event) {
      event.preventDefault(); if (submit.disabled) return; submit.disabled = true; feedback.textContent = "";
      var values = Object.fromEntries(new FormData(form));
      onSubmit(values).then(function () { form.reset(); closeDialog(backdrop); requestMembers(true); requestInvitations(true); }).catch(function (message) { feedback.textContent = message; }).finally(function () { submit.disabled = false; });
    });
  }

  function showInvite() {
    sensitiveDialog("Invite teammate", [{ label: "Work email", name: "email", type: "email" }, { label: "Role", name: "role", options: ["Administrator", "Operations", "Billing", "Support"] }], "Send invitation", function (values) {
      return postJSON("/api/v1/team/invitations", "POST", values).then(function (response) { if (!response.ok) return safeError(response).then(Promise.reject.bind(Promise)); return response.json(); }).then(function (payload) { if (payload && payload.invitation) issuedInvitations = reconcileInvitation(issuedInvitations, payload.invitation, payload.invitation.id); });
    });
  }

  function showResellerDialog() {
    sensitiveDialog("Set customer reseller status", [{ label: "Customer account email", name: "email", type: "email" }, { label: "Action", name: "action", options: ["Make reseller", "Remove reseller"] }], "Save reseller status", function (values) {
      return fetch(apiBase + "/api/v1/users/lookup?email=" + encodeURIComponent(String(values.email || "").trim()), { credentials: "include", cache: "no-store" })
        .then(function (response) { if (!response.ok) return safeError(response).then(Promise.reject.bind(Promise)); return response.json(); })
        .then(function (payload) {
          if (!payload || !payload.data || !payload.data.id) return Promise.reject("This customer account could not be found.");
          return postJSON("/api/v1/users/" + encodeURIComponent(payload.data.id) + "/reseller", "PUT", { enabled: values.action === "Make reseller", password: values.password, mfa_code: values.mfa_code });
        }).then(function (response) { if (!response.ok) return safeError(response).then(Promise.reject.bind(Promise)); });
    });
  }

  function bindHeaderAction() {
    if (!canWrite() || currentPage() !== "team") return;
    var actions = document.querySelector("#page-content .heading-actions");
    if (!actions || actions.querySelector(".team-invite")) return;
    actions.insertAdjacentHTML("beforeend", renderTeamActions(window.NETCORE_PRINCIPAL));
    actions.querySelector(".team-invite").addEventListener("click", showInvite);
    var reseller = document.createElement("button"); reseller.type = "button"; reseller.className = "button team-reseller"; reseller.textContent = "Set reseller status";
    reseller.addEventListener("click", showResellerDialog); actions.append(reseller);
  }

  function displayIssuedInvitations() {
    if (!canWrite() || !issuedInvitations.length || currentPage() !== "team") return;
    var table = document.querySelector("#page-content .data-table");
    if (!table) return;
    issuedInvitations.forEach(function (invitation) {
      var row = document.createElement("tr");
      appendTextCell(row, invitation.email); appendTextCell(row, invitation.role); appendTextCell(row, "Not yet active"); appendTextCell(row, "Set up on acceptance"); appendTextCell(row, invitation.status || "Pending");
      var controls = document.createElement("td");
      [["Resend", "POST", "/api/v1/team/invitations/" + invitation.id + "/resend"], ["Revoke", "DELETE", "/api/v1/team/invitations/" + invitation.id]].forEach(function (action) { var button = document.createElement("button"); button.type = "button"; button.className = "button team-row-action"; button.textContent = action[0]; button.onclick = function () { sensitiveDialog(action[0] + " invitation", [], action[0], function (values) { return postJSON(action[2], action[1], values).then(function (response) { if (!response.ok) return safeError(response).then(Promise.reject.bind(Promise)); return action[0] === "Resend" ? response.json().then(function (payload) { issuedInvitations = reconcileInvitation(issuedInvitations, payload.invitation, invitation.id); }) : (issuedInvitations = reconcileInvitation(issuedInvitations, null, invitation.id)); }); }); }; controls.appendChild(button); });
      row.appendChild(controls); table.querySelector("tbody").appendChild(row);
    });
  }

  function currentPage() {
    return livePage.current();
  }

  function showState(state, options) {
    livePage.showState("team", state, options);
  }

  function safeText(value) {
    return value == null || value === "" ? "—" : String(value);
  }

  function showOperationNotice(message) {
    var toolbar = document.querySelector("#page-content .toolbar");
    if (!toolbar) return;
    var notice = toolbar.querySelector(".team-operation-notice");
    if (!notice) {
      notice = document.createElement("p");
      notice.className = "team-operation-notice description";
      notice.setAttribute("role", "status");
      toolbar.appendChild(notice);
    }
    notice.textContent = message;
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
    if (canWrite()) headings.push("Select", "Actions");
    var headingRow = table.querySelector("thead tr");
    headingRow.replaceChildren();
    headings.forEach(function (value) {
      var heading = document.createElement("th");
      heading.textContent = value;
      headingRow.appendChild(heading);
    });
  }

  function renderBulkActions() {
    if (!canWrite() || currentPage() !== "team") return;
    var toolbar=document.querySelector("#page-content .toolbar"); if(!toolbar)return; var old=toolbar.querySelector(".team-bulk-actions"); if(old)old.remove(); var wrap=document.createElement("div"); wrap.className="team-bulk-actions";
    [["Disable and sign out selected",false],["Reactivate selected",true]].forEach(function(action){var button=document.createElement("button");button.type="button";button.className="button team-row-action";button.textContent=action[0];button.disabled=selectedMemberIDs.size===0;button.onclick=function(){var note=action[1]?"Reactivate the selected staff accounts. Previously issued sessions remain invalid and staff must sign in again.":"This immediately disables each selected account and signs it out from all devices.";sensitiveDialog(action[0]+" members",[],action[0],function(values){values.user_ids=Array.from(selectedMemberIDs);values.active=action[1];return postJSON("/api/v1/team/members/bulk-lifecycle","POST",values).then(function(response){if(!response.ok)return safeError(response).then(Promise.reject.bind(Promise));selectedMemberIDs.clear();});},note);};wrap.appendChild(button);});toolbar.appendChild(wrap);
  }

  function statusLabel(value) {
    return safeText(value).toLowerCase().replace(/_/g, " ").replace(/\b\w/g, function (letter) { return letter.toUpperCase(); });
  }

  function roleOptions() {
    return teamRoles.map(function (role) {
      return { value: role, label: role === "" ? "All roles" : role === "UNASSIGNED" ? "No role assigned" : role };
    });
  }

  function applyTeamCriteria(query, status, role, refresh) {
    var changed = window.NetCoreLiveListControls.applyCriteria(listState, query, status);
    var nextRole = teamRoles.indexOf(role) >= 0 ? role : "";
    if (roleFilter !== nextRole) {
      roleFilter = nextRole;
      listState.cursor = "";
      listState.previousCursors = [];
      listState.nextCursor = "";
      listState.hasMore = false;
      changed = true;
    }
    if (changed && refresh) {
      requestVersion += 1;
      requestMembers(true);
    }
  }

  function memberRequestURL() {
    var url = new URL(window.NetCoreLiveListControls.requestURL(apiBase, listConfig.endpoint, listState, 25));
    if (roleFilter) url.searchParams.set("role", roleFilter);
    return url.toString();
  }

  function renderControls() {
    if (currentPage() !== "team") return;
    livePage.renderListControls("team", {
      query: criteriaPending ? pendingQuery : listState.query,
      filter: listState.filter,
      filters: listConfig.filters.map(function (status) { return { value: status, label: status ? statusLabel(status) : "All access states" }; }),
      busy: requestInFlight,
      hasPrevious: listState.previousCursors.length > 0,
      hasNext: listState.hasMore,
      searchPlaceholder: "Search team members",
      searchLabel: "Search team members by email, phone, or role",
      filterLabel: "Filter access state",
      onSearch: function (query) {
        clearTimeout(searchTimer);
        pendingQuery = query;
        criteriaPending = true;
        requestVersion += 1;
        applyTeamCriteria(query, listState.filter, roleFilter, false);
        searchTimer = setTimeout(function () {
          criteriaPending = false;
          requestVersion += 1;
          requestMembers(true);
        }, 250);
      },
      onFilter: function (status) { applyTeamCriteria(listState.query, status, roleFilter, true); },
      onNext: function () { if (criteriaPending) return; if (window.NetCoreLiveListControls.nextPage(listState)) requestMembers(true); else renderControls(); },
      onPrevious: function () { if (criteriaPending) return; if (listState.previousCursors.length) { window.NetCoreLiveListControls.previousPage(listState); requestMembers(true); } else renderControls(); }
    });
    var mount = livePage.listMount("team");
    if (!mount) return;
    var role = document.createElement("select");
    role.setAttribute("aria-label", "Filter team role");
    role.disabled = requestInFlight;
    roleOptions().forEach(function (option) {
      var choice = document.createElement("option");
      choice.value = option.value;
      choice.textContent = option.label;
      choice.selected = option.value === roleFilter;
      role.appendChild(choice);
    });
    role.addEventListener("change", function () { if (!requestInFlight) applyTeamCriteria(listState.query, listState.filter, role.value, true); });
    mount.appendChild(role);
  }

  function displayMembers() {
    if (!loadedMembers || currentPage() !== "team") return;
    var table = document.querySelector("#page-content .data-table");
    if (!table) return;

    setHeadings(table);
    var body = table.querySelector("tbody");
    body.replaceChildren();
    if (loadedMembers.length === 0) {
      displayIssuedInvitations();
      if (issuedInvitations.length) {
        showState("records");
        return;
      }
      showState("empty", { message: "No team members match this view." });
      return;
    }

    loadedMembers.forEach(function (member) {
      var row = document.createElement("tr");
      appendMemberCell(row, member);
      appendTextCell(row, Array.isArray(member.roles) && member.roles.length ? member.roles.join(", ") : "No role assigned");
      appendTextCell(row, relativeTime(member.last_seen_at));
      appendTagCell(row, member.mfa_status, mfaClass(member.mfa_status));
      appendTagCell(row, member.status, accountClass(member.status));
      if (canWrite()) {
        var select=document.createElement("td"), checkbox=document.createElement("input"); checkbox.type="checkbox"; checkbox.checked=selectedMemberIDs.has(member.id); checkbox.setAttribute("aria-label","Select "+displayName(member)); checkbox.onchange=(function(input,id){return function(){if(input.checked)selectedMemberIDs.add(id);else selectedMemberIDs.delete(id);renderBulkActions();};})(checkbox,member.id); select.appendChild(checkbox); row.appendChild(select);
        var actions = document.createElement("td");
        var memberActions = [
          ["Change role", "PUT", "/api/v1/team/members/" + member.id + "/role", "role"],
          [member.status === "DISABLED" ? "Reactivate" : "Disable and sign out", "POST", "/api/v1/team/members/" + member.id + (member.status === "DISABLED" ? "/reactivate" : "/deactivate"), ""]
        ];
        if (member.status !== "DISABLED") memberActions.push(["Sign out everywhere", "POST", "/api/v1/team/members/" + member.id + "/sign-out", "logout"]);
        if (!window.NETCORE_PRINCIPAL || window.NETCORE_PRINCIPAL.id !== member.id) memberActions.push(["Reset password", "POST", "/api/v1/team/members/" + member.id + "/password-reset", "password"]);
        memberActions.forEach(function (action) {
          var button = document.createElement("button"); button.type = "button"; button.className = "button team-row-action"; button.textContent = action[0];
          button.addEventListener("click", function () { var fields = action[3] === "role" ? [{ label: "New role", name: "role", options: ["Administrator", "Operations", "Billing", "Support"] }] : action[3] === "password" ? [{ label: "New password (at least 16 characters)", name: "new_password", type: "password", minLength: 16, maxLength: 1024 }] : []; var note=action[3] === "logout" ? "This keeps the account active but immediately signs the staff member out from all devices." : action[0] === "Disable and sign out" ? "This immediately disables the account and signs the staff member out from all devices." : action[0] === "Reactivate" ? "This restores the account, but previously issued sessions remain invalid and the staff member must sign in again." : ""; sensitiveDialog(action[0] + " member", fields, action[0], function (values) { return postJSON(action[2], action[1], values).then(function (response) { if (!response.ok) return safeError(response).then(Promise.reject.bind(Promise)); if (action[3] === "logout") return response.json().then(function (payload) { showOperationNotice("Staff member signed out from " + safeText(payload && payload.sessions_invalidated) + " current session(s)."); }); if (action[0] === "Disable and sign out") showOperationNotice("Staff account disabled and signed out from all current sessions."); }); },note); }); actions.appendChild(button);
        }); row.appendChild(actions);
      }
      body.appendChild(row);
    });
    displayIssuedInvitations();
    showState("records");
    renderBulkActions();
  }

  function requestMembers(force) {
    if (requestInFlight || (loadedMembers && !force)) return;
    requestInFlight = true;
    var requestVersionAtStart = requestVersion;
    renderControls();
    if (!loadedMembers) showState("loading");
    fetch(memberRequestURL(), {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) throw new Error("Team request failed");
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) throw new Error("Team response was invalid");
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        loadedMembers = payload.data;
        loadedMembersMeta = payload.meta || {};
        window.NetCoreLiveListControls.applyResponseMeta(listState, loadedMembersMeta);
        displayMembers();
      })
      .catch(function () {
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        // Last verified records remain visible when a refresh fails.
        if (loadedMembers) displayMembers();
        showState("error", { message: "Team members could not be loaded. Please try again.", preserve: Boolean(loadedMembers), retry: function () { requestMembers(true); } });
      })
      .finally(function () {
        requestInFlight = false;
        renderControls();
        if (!criteriaPending && requestVersionAtStart !== requestVersion) requestMembers(true);
      });
  }

  function requestInvitations(force) {
    if (!canWrite() || invitationRequestInFlight || (issuedInvitations.length && !force)) return;
    invitationRequestInFlight = true;
    fetch(apiBase + "/api/v1/team/invitations", { credentials: "same-origin", cache: "no-store", headers: { "Accept": "application/json" } })
      .then(function (response) { if (!response.ok) throw new Error("Invitation request failed"); return response.json(); })
      .then(function (payload) { if (!payload || !Array.isArray(payload.data)) throw new Error("Invitation response was invalid"); issuedInvitations = payload.data; displayMembers(); })
      .catch(function () { /* Existing safe projections remain visible during a refresh failure. */ })
      .finally(function () { invitationRequestInFlight = false; });
  }

  function onPageRendered(event) {
    if (event.detail !== "team") return;
    bindHeaderAction();
    renderBulkActions();
    renderControls();
    requestInvitations(true);
    if (loadedMembers) requestMembers(true);
    else requestMembers();
  }

  livePage.subscribe(function (page) { onPageRendered({ detail: page }); });
  if (currentPage() === "team") onPageRendered({ detail: "team" });
}());
