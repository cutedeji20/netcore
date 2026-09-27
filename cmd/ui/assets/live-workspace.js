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

  function canWriteVerificationPolicy() {
    var principal = window.NETCORE_PRINCIPAL;
    return Boolean(principal && Array.isArray(principal.permissions) && principal.permissions.indexOf("workspace.write") !== -1);
  }

  function canResetTestData() {
    var principal = window.NETCORE_PRINCIPAL;
    return Boolean(workspace && workspace.test_data_reset_enabled && principal && Array.isArray(principal.permissions) && principal.permissions.indexOf("tenant.test_data_reset") !== -1);
  }

  function resetError(response, fallback) {
    return response.json().catch(function () { return {}; }).then(function (body) {
      throw new Error((body && body.error && body.error.message) || fallback);
    });
  }

  function field(labelText, name, type) {
    var label = document.createElement("label"); label.className = "test-data-reset-field"; label.textContent = labelText;
    var input = document.createElement("input"); input.name = name; input.type = type || "text"; input.required = true;
    label.appendChild(input); return label;
  }

  function openResetDialog(preview) {
    var backdrop = document.createElement("div"); backdrop.className = "test-data-reset-backdrop";
    var form = document.createElement("form"); form.className = "test-data-reset-dialog";
    var title = document.createElement("h2"); title.textContent = "Reset test data";
    var note = document.createElement("p"); note.textContent = "This permanently removes test customers, successful test payments, invoices, subscriptions, devices, sessions, RADIUS accounting, vouchers and ledger data. Plans, staff, router/NAS/AP settings and integrations remain.";
    var summary = document.createElement("p"); summary.className = "test-data-reset-summary";
    summary.textContent = [preview.customers + " customers", preview.subscriptions + " subscriptions", preview.payments + " payments", preview.invoices + " invoices", preview.devices + " devices"].join(" · ");
    var backup = field("Verified database backup reference", "backup_reference"); backup.querySelector("input").placeholder = "e.g. netcore-before-live-2026-09-27.dump";
    var reason = field("Reason", "reason"); reason.querySelector("input").maxLength = 240;
    var confirmation = field('Type "RESET ' + workspace.slug + '" to continue', "confirmation");
    var password = field("Your current password", "password", "password");
    var mfa = field("Authenticator code", "mfa_code"); mfa.querySelector("input").inputMode = "numeric"; mfa.querySelector("input").maxLength = 6;
    var feedback = document.createElement("p"); feedback.className = "test-data-reset-feedback"; feedback.setAttribute("role", "alert");
    var footer = document.createElement("footer"); var cancel = document.createElement("button"); cancel.type = "button"; cancel.className = "button"; cancel.textContent = "Cancel";
    var submit = document.createElement("button"); submit.type = "submit"; submit.className = "button test-data-reset-confirm"; submit.textContent = "Permanently reset test data";
    cancel.onclick = function () { backdrop.remove(); }; footer.append(cancel, submit);
    form.append(title, note, summary, backup, reason, confirmation, password, mfa, feedback, footer); backdrop.appendChild(form); document.body.appendChild(backdrop);
    form.addEventListener("submit", function (event) {
      event.preventDefault(); feedback.textContent = ""; submit.disabled = true;
      var data = new FormData(form);
      fetch(apiBase + "/api/v1/workspace/test-data-reset", { method: "POST", credentials: "include", headers: { "Content-Type": "application/json" }, body: JSON.stringify({
        backup_reference: String(data.get("backup_reference") || ""), reason: String(data.get("reason") || ""), confirmation: String(data.get("confirmation") || ""), password: String(data.get("password") || ""), mfa_code: String(data.get("mfa_code") || "")
      }) }).then(function (response) { if (!response.ok) return resetError(response, "The reset was not completed."); return response.json(); })
        .then(function () { backdrop.remove(); workspace = null; requestWorkspace(true); if (window.NetCoreToast) window.NetCoreToast.show("Test data was reset. Keep the backup until live operations are verified."); })
        .catch(function (error) { feedback.textContent = error.message || "The reset was not completed."; submit.disabled = false; });
    });
  }

  function addTestDataResetControls(content) {
    var existing = content.querySelector(".test-data-reset-controls");
    if (!canResetTestData()) { if (existing) existing.remove(); return; }
    if (existing) return;
    var controls = document.createElement("section"); controls.className = "test-data-reset-controls";
    var title = document.createElement("strong"); title.textContent = "Danger zone — pre-live reset";
    var copy = document.createElement("p"); copy.textContent = "Permanently clear test customer and commercial data while preserving plans and network configuration. A verified database backup, your password and MFA are required.";
    var action = document.createElement("button"); action.type = "button"; action.className = "button test-data-reset-button"; action.textContent = "Reset test data";
    var feedback = document.createElement("p"); feedback.className = "test-data-reset-feedback";
    action.onclick = function () {
      action.disabled = true; feedback.textContent = "Loading reset preview…";
      fetch(apiBase + "/api/v1/workspace/test-data-reset/preview", { credentials: "include", cache: "no-store" })
        .then(function (response) { if (!response.ok) return resetError(response, "The reset preview could not be loaded."); return response.json(); })
        .then(function (body) { var preview = body.data || {}; if (Number(preview.active_sessions || 0) || Number(preview.active_reservations || 0) || Number(preview.unpublished_outbox || 0)) { feedback.textContent = "Reset is blocked: close active sessions and wait for queued tenant events first."; return; } feedback.textContent = ""; openResetDialog(preview); })
        .catch(function (error) { feedback.textContent = error.message || "The reset preview could not be loaded."; })
        .finally(function () { action.disabled = false; });
    };
    controls.append(title, copy, action, feedback); content.appendChild(controls);
  }

  function addVerificationPolicyControls(content) {
    var controls = content.querySelector(".verification-policy-controls");
    if (!canWriteVerificationPolicy()) {
      if (controls) controls.remove();
      return;
    }
    if (!controls) {
      controls = document.createElement("div");
      controls.className = "verification-policy-controls";
      controls.innerHTML = "<strong>Customer registration verification</strong><p>Email verification can be enabled now. Phone verification remains unavailable until an SMS provider is configured.</p>";
      [["Require email verification", "require_email_verification"], ["Require phone verification", "require_phone_verification"]].forEach(function (item) {
        var label = document.createElement("label");
        var input = document.createElement("input");
        input.type = "checkbox";
        input.name = item[1];
        input.disabled = item[1] === "require_phone_verification";
        label.append(input, document.createTextNode(" " + item[0]));
        controls.appendChild(label);
      });
      var feedback = document.createElement("p");
      feedback.className = "verification-policy-feedback";
      controls.appendChild(feedback);
      var save = document.createElement("button");
      save.type = "button";
      save.className = "button primary";
      save.textContent = "Save verification policy";
      save.onclick = function () {
        feedback.textContent = "";
        save.disabled = true;
        fetch(apiBase + "/api/v1/workspace/verification-policy", {
          method: "PUT", credentials: "include", headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            require_email_verification: controls.querySelector('[name=require_email_verification]').checked,
            require_phone_verification: controls.querySelector('[name=require_phone_verification]').checked
          })
        }).then(function (response) {
          if (!response.ok) throw new Error("save failed");
          return response.json();
        }).then(function (next) {
          workspace = next;
          displayWorkspace();
          feedback.textContent = "Saved.";
        }).catch(function () {
          feedback.textContent = "The policy was not saved. Please try again.";
        }).finally(function () { save.disabled = false; });
      };
      controls.appendChild(save);
      content.querySelector(".panel.table").appendChild(controls);
    }
    controls.querySelector('[name=require_email_verification]').checked = Boolean(workspace.require_email_verification);
    controls.querySelector('[name=require_phone_verification]').checked = Boolean(workspace.require_phone_verification);
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
    appendRow(body, "Require email verification", workspace.require_email_verification ? "On" : "Off");
    appendRow(body, "Require phone verification", workspace.require_phone_verification ? "On" : "Off");

    addVerificationPolicyControls(content);
    addTestDataResetControls(content);
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
