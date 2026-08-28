(function () {
  "use strict";

  var loadedPlans = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var livePage = window.NetCoreLivePage;
  var currencyExponents = { JPY: 0, KRW: 0, VND: 0, CLP: 0, ISK: 0, XAF: 0, XOF: 0, BHD: 3, KWD: 3, OMR: 3, TND: 3, JOD: 3 };

  function currentPage() { return livePage.current(); }

  function showState(state, options) { livePage.showState("plans", state, options); }

  function canWritePlans() {
    var permissions = window.NETCORE_PRINCIPAL && window.NETCORE_PRINCIPAL.permissions;
    return Array.isArray(permissions) && permissions.indexOf("plan.write") !== -1;
  }

  function canDeletePlans() {
    var permissions = window.NETCORE_PRINCIPAL && window.NETCORE_PRINCIPAL.permissions;
    return Array.isArray(permissions) && permissions.indexOf("plan.delete") !== -1;
  }

  function canManagePlans() { return canWritePlans() || canDeletePlans(); }

  function safeText(value) { return value == null || value === "" ? "—" : String(value); }

  function formatRate(bps) {
    var megabits = Number(bps) / 1000000;
    if (!Number.isFinite(megabits)) return "—";
    return (Number.isInteger(megabits) ? megabits : megabits.toFixed(1)) + " Mbps";
  }

  function formatDuration(seconds) {
    var days = Math.round(Number(seconds) / 86400);
    return Number.isFinite(days) && days > 0 ? days + (days === 1 ? " day" : " days") : "—";
  }

  function formatPrice(minor, currency) {
    var unit = String(currency || "—").toUpperCase();
    var raw = String(minor == null ? "0" : minor);
    if (!/^-?\d+$/.test(raw)) return "—";
    var negative = raw.charAt(0) === "-";
    raw = negative ? raw.slice(1) : raw;
    var exponent = Object.prototype.hasOwnProperty.call(currencyExponents, unit) ? currencyExponents[unit] : 2;
    while (raw.length <= exponent) raw = "0" + raw;
    var whole = exponent ? raw.slice(0, -exponent) : raw;
    var fraction = exponent ? "." + raw.slice(-exponent) : "";
    whole = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
    return (negative ? "-" : "") + unit + " " + whole + fraction;
  }

  function statusClass(status) { return status === "ACTIVE" ? "green" : "gray"; }
  function statusLabel(status) { return status === "ACTIVE" ? "Published" : "Retired"; }

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

  function appendPlanCell(row, plan) {
    var cell = document.createElement("td");
    var entity = document.createElement("div");
    var mark = document.createElement("span");
    var copy = document.createElement("span");
    var name = document.createElement("strong");
    var detail = document.createElement("small");
    entity.className = "entity";
    mark.className = "entity-mark";
    mark.textContent = String(plan.name || "?").trim().slice(0, 2).toUpperCase();
    name.textContent = safeText(plan.name);
    detail.textContent = plan.description || (plan.max_devices + " devices · " + plan.max_concurrent_sessions + " sessions");
    copy.append(name, detail);
    entity.append(mark, copy);
    cell.appendChild(entity);
    row.appendChild(cell);
  }

  function actionButton(label, className, listener) {
    var button = document.createElement("button");
    button.className = "button " + className;
    button.type = "button";
    button.textContent = label;
    button.addEventListener("click", listener);
    return button;
  }

  function appendActionCell(row, plan) {
    var cell = document.createElement("td");
    cell.className = "plan-actions";
    if (canWritePlans()) {
      cell.appendChild(actionButton("Edit", "plan-edit", function () { openPlanDialog(plan); }));
      if (plan.status === "ACTIVE") {
        cell.appendChild(actionButton("Retire", "plan-retire", function () { openPublicationDialog(plan, "RETIRED"); }));
      } else {
        cell.appendChild(actionButton("Restore", "plan-restore", function () { openPublicationDialog(plan, "ACTIVE"); }));
      }
    }
    if (canDeletePlans()) cell.appendChild(actionButton("Delete", "plan-delete", function () { openDeletePlanDialog(plan); }));
    row.appendChild(cell);
  }

  function setHeadings(table) {
    var headings = ["Plan", "Speed", "Price", "Duration", "Subscribers", "Status"];
    if (canManagePlans()) headings.push("Action");
    var headingRow = table.querySelector("thead tr");
    headingRow.replaceChildren();
    headings.forEach(function (value) {
      var heading = document.createElement("th");
      heading.textContent = value;
      headingRow.appendChild(heading);
    });
  }

  function addCreateButton() {
    var heading = document.querySelector("#page-content .page-heading");
    if (!heading || !canWritePlans() || heading.querySelector(".plan-create")) return;
    var actions = document.createElement("div");
    var create = document.createElement("button");
    actions.className = "heading-actions";
    create.className = "button primary plan-create";
    create.type = "button";
    create.textContent = "＋ Create plan";
    create.addEventListener("click", function () { openPlanDialog(null); });
    actions.appendChild(create);
    heading.appendChild(actions);
  }

  function displayPlans() {
    if (!loadedPlans || currentPage() !== "plans") return;
    var table = document.querySelector("#page-content .data-table");
    if (!table) return;
    addCreateButton();
    setHeadings(table);
    var body = table.querySelector("tbody");
    body.replaceChildren();
    if (loadedPlans.length === 0) {
      showState("empty", { message: canWritePlans() ? "No plans yet. Create your first published plan." : "No plans match this view." });
      return;
    }
    loadedPlans.forEach(function (plan) {
      var row = document.createElement("tr");
      appendPlanCell(row, plan);
      appendTextCell(row, formatRate(plan.download_bps) + " / " + formatRate(plan.upload_bps));
      appendTextCell(row, formatPrice(plan.price_minor, plan.currency));
      appendTextCell(row, formatDuration(plan.duration_seconds));
      appendTextCell(row, plan.active_subscriptions);
      appendStatusCell(row, plan.status);
      if (canManagePlans()) appendActionCell(row, plan);
      body.appendChild(row);
    });
    showState("records");
  }

  function requestPlans(force) {
    if (requestInFlight || (loadedPlans && !force)) return;
    requestInFlight = true;
    if (!loadedPlans) showState("loading");
    fetch(apiBase + "/api/v1/plans?limit=100", {
      credentials: "include", cache: "no-store", headers: { "Accept": "application/json" }
    }).then(function (response) {
      if (!response.ok) throw new Error("Plans could not be loaded.");
      return response.json();
    }).then(function (payload) {
      if (!payload || !Array.isArray(payload.data)) throw new Error("Plans response was invalid");
      loadedPlans = payload.data;
      displayPlans();
    }).catch(function (error) {
      // Last verified records remain visible when a refresh fails.
      if (loadedPlans) displayPlans();
      showState("error", { message: "Plans could not be loaded. Please try again.", preserve: Boolean(loadedPlans), retry: function () { requestPlans(true); } });
      showPlanMessage(error && error.message ? error.message : "Plans could not be loaded.", true);
    }).finally(function () { requestInFlight = false; });
  }

  function numberString(value, factor) {
    var number = Number(value || 0) / factor;
    return Number.isFinite(number) ? String(number) : "";
  }

  function minorToMoney(value) {
    var raw = String(value || "0");
    if (!/^\d+$/.test(raw)) return "";
    while (raw.length < 3) raw = "0" + raw;
    return raw.slice(0, -2) + "." + raw.slice(-2);
  }

  function createInput(label, name, type, value, options) {
    var field = document.createElement("label");
    var input = document.createElement(type === "select" ? "select" : "input");
    field.textContent = label;
    field.className = "plan-field";
    input.name = name;
    input.required = !(options && options.required === false);
    if (type !== "select") input.type = type;
    if (options && options.step) input.step = options.step;
    if (options && options.min != null) input.min = String(options.min);
    if (options && options.max != null) input.max = String(options.max);
    if (type === "select") {
      (options && options.values || []).forEach(function (item) {
        var option = document.createElement("option");
        option.value = item.value;
        option.textContent = item.label;
        input.appendChild(option);
      });
    }
    input.value = value == null ? "" : String(value);
    field.appendChild(input);
    return field;
  }

  function openPlanDialog(plan) {
    if (!canWritePlans()) return;
    closePlanDialog();
    var hasSubscriptions = Boolean(plan && Number(plan.active_subscriptions) > 0);
    var backdrop = document.createElement("div");
    var dialog = document.createElement("section");
    var heading = document.createElement("header");
    var title = document.createElement("h2");
    var note = document.createElement("p");
    var form = document.createElement("form");
    var feedback = document.createElement("p");
    var footer = document.createElement("footer");
    var cancel = document.createElement("button");
    var submit = document.createElement("button");
    var current = plan || {};

    backdrop.className = "plan-dialog-backdrop";
    backdrop.id = "plan-dialog-backdrop";
    dialog.className = "plan-dialog";
    title.textContent = plan ? "Edit plan" : "Create plan";
    note.className = "plan-dialog-note";
    note.textContent = hasSubscriptions ? "This plan has subscription history, so its commercial terms are locked. Use Retire or Restore outside this form to control future sales." : "Published plans become visible in the customer portal once checkout is enabled.";
    heading.append(title, note);
    form.className = "plan-form";
    form.noValidate = true;
    form.append(
      createInput("Plan name", "name", "text", current.name, { max: 120 }),
      createInput("Description", "description", "text", current.description, { required: false, max: 1000 }),
      createInput("Price (NGN)", "price", "text", minorToMoney(current.price_minor), {}),
      createInput("Duration (days)", "duration_days", "number", current.duration_seconds ? Number(current.duration_seconds) / 86400 : 1, { min: 1, max: 1825, step: 1 }),
      createInput("Download speed (Mbps)", "download_mbps", "number", numberString(current.download_bps, 1000000), { min: 0.1, max: 10000, step: 0.1 }),
      createInput("Upload speed (Mbps)", "upload_mbps", "number", numberString(current.upload_bps, 1000000), { min: 0.1, max: 10000, step: 0.1 }),
      createInput("Registered devices", "max_devices", "number", current.max_devices || 1, { min: 1, max: 100, step: 1 }),
      createInput("Concurrent sessions", "max_sessions", "number", current.max_concurrent_sessions || 1, { min: 1, max: 100, step: 1 }),
      createInput("Data cap (GB, optional)", "quota_gb", "text", current.quota_bytes ? numberString(current.quota_bytes, 1000000000) : "", { required: false })
    );
    if (hasSubscriptions) {
      Array.prototype.forEach.call(form.elements, function (field) {
        if (field.name !== "status") field.readOnly = true;
      });
    }
    feedback.className = "plan-form-feedback";
    feedback.setAttribute("role", "alert");
    cancel.className = "button";
    cancel.type = "button";
    cancel.textContent = "Cancel";
    cancel.addEventListener("click", closePlanDialog);
    submit.className = "button primary";
    submit.type = hasSubscriptions ? "button" : "submit";
    submit.textContent = hasSubscriptions ? "Close" : (plan ? "Save changes" : "Create plan");
    if (hasSubscriptions) submit.addEventListener("click", closePlanDialog);
    footer.append(cancel, submit);
    form.append(feedback, footer);
    if (!hasSubscriptions) form.addEventListener("submit", function (event) {
      event.preventDefault();
      submitPlan(plan, current, form, submit, feedback);
    });
    dialog.append(heading, form);
    backdrop.appendChild(dialog);
    backdrop.addEventListener("click", function (event) { if (event.target === backdrop) closePlanDialog(); });
    document.body.appendChild(backdrop);
    form.elements.name.focus();
  }

  function exactMinor(value) {
    var text = String(value || "").trim();
    var matched = /^(0|[1-9]\d*)(?:\.(\d{1,2}))?$/.exec(text);
    if (!matched) return null;
    var fraction = (matched[2] || "").padEnd(2, "0");
    var minor = BigInt(matched[1]) * 100n + BigInt(fraction);
    return minor <= 9223372036854775807n ? minor.toString() : null;
  }

  function quotaBytes(value) {
    var text = String(value || "").trim();
    if (text === "") return null;
    if (!/^(0|[1-9]\d*)(?:\.\d{1,3})?$/.test(text)) return undefined;
    var bytes = Number(text) * 1000000000;
    return Number.isSafeInteger(bytes) && bytes > 0 ? bytes : undefined;
  }

  function planPayload(current, form) {
    var values = new FormData(form);
    var minor = exactMinor(values.get("price"));
    var download = Math.round(Number(values.get("download_mbps")) * 1000000);
    var upload = Math.round(Number(values.get("upload_mbps")) * 1000000);
    var quota = quotaBytes(values.get("quota_gb"));
    var durationDays = Number(values.get("duration_days"));
    var devices = Number(values.get("max_devices"));
    var sessions = Number(values.get("max_sessions"));
    if (minor == null || !Number.isInteger(durationDays) || durationDays < 1 || durationDays > 1825 || !Number.isSafeInteger(download) || download < 100000 || !Number.isSafeInteger(upload) || upload < 100000 || !Number.isInteger(devices) || devices < 1 || devices > 100 || !Number.isInteger(sessions) || sessions < 1 || sessions > devices || quota === undefined) return null;
    return { name: String(values.get("name") || ""), description: String(values.get("description") || ""), price_minor: minor, currency: "NGN", duration_seconds: durationDays * 86400, download_bps: download, upload_bps: upload, max_devices: devices, max_concurrent_sessions: sessions, quota_bytes: quota, quota_reset_policy: quota == null ? "NONE" : "PER_SUBSCRIPTION", status: current && current.status ? current.status : "ACTIVE" };
  }

  function submitPlan(plan, current, form, submit, feedback) {
    var payload = planPayload(plan ? current : null, form);
    if (!payload || !payload.name.trim()) {
      feedback.textContent = "Enter a valid name, NGN price, duration, speeds, and device limits.";
      return;
    }
    submit.disabled = true;
    feedback.textContent = "Saving plan…";
    fetch(apiBase + "/api/v1/plans" + (plan ? "/" + encodeURIComponent(plan.id) : ""), {
      method: plan ? "PUT" : "POST", credentials: "include", cache: "no-store", headers: { "Content-Type": "application/json", "Accept": "application/json" }, body: JSON.stringify(payload)
    }).then(function (response) {
      return response.json().catch(function () { return {}; }).then(function (body) {
        if (!response.ok) throw new Error(body && body.error && body.error.message ? body.error.message : "The plan could not be saved.");
        return body;
      });
    }).then(function () {
      closePlanDialog();
      requestPlans(true);
    }).catch(function (error) {
      feedback.textContent = error && error.message ? error.message : "The plan could not be saved.";
    }).finally(function () { submit.disabled = false; });
  }

  function closePlanLifecycleDialog() {
    var dialog = document.querySelector("#plan-lifecycle-dialog-backdrop");
    if (dialog) dialog.remove();
  }

  function runPlanLifecycle(plan, endpoint, method, submit, feedback, success) {
    submit.disabled = true;
    feedback.textContent = "Saving…";
    fetch(apiBase + "/api/v1/plans/" + encodeURIComponent(plan.id) + endpoint, {
      method: method, credentials: "include", cache: "no-store", headers: { "Accept": "application/json" }
    }).then(function (response) {
      return response.json().catch(function () { return {}; }).then(function (body) {
        if (!response.ok) throw new Error(body && body.error && body.error.message ? body.error.message : "The plan could not be changed.");
      });
    }).then(function () {
      closePlanLifecycleDialog();
      showPlanMessage(success, false);
      requestPlans(true);
    }).catch(function (error) {
      feedback.textContent = error && error.message ? error.message : "The plan could not be changed.";
    }).finally(function () { submit.disabled = false; });
  }

  function lifecycleDialog(titleText, noteText, submitText, onSubmit) {
    closePlanLifecycleDialog();
    var backdrop = document.createElement("div");
    var dialog = document.createElement("section");
    var heading = document.createElement("header");
    var title = document.createElement("h2");
    var note = document.createElement("p");
    var feedback = document.createElement("p");
    var footer = document.createElement("footer");
    var cancel = document.createElement("button");
    var submit = document.createElement("button");
    backdrop.className = "plan-dialog-backdrop";
    backdrop.id = "plan-lifecycle-dialog-backdrop";
    dialog.className = "plan-dialog plan-lifecycle-dialog";
    title.textContent = titleText;
    note.className = "plan-dialog-note";
    note.textContent = noteText;
    feedback.className = "plan-form-feedback";
    feedback.setAttribute("role", "alert");
    cancel.className = "button";
    cancel.type = "button";
    cancel.textContent = "Cancel";
    cancel.addEventListener("click", closePlanLifecycleDialog);
    submit.className = "button primary";
    submit.type = "button";
    submit.textContent = submitText;
    submit.addEventListener("click", function () { onSubmit(submit, feedback); });
    heading.append(title, note);
    footer.append(cancel, submit);
    dialog.append(heading, feedback, footer);
    backdrop.appendChild(dialog);
    backdrop.addEventListener("click", function (event) { if (event.target === backdrop) closePlanLifecycleDialog(); });
    document.body.appendChild(backdrop);
  }

  function openPublicationDialog(plan, targetStatus) {
    if (!canWritePlans()) return;
    var retiring = targetStatus === "RETIRED";
    var action = retiring ? "Retire" : "Restore";
    var note = retiring
      ? "This removes “" + safeText(plan.name) + "” from new purchases. Existing active subscriptions keep access until their normal expiry, cancellation, or suspension."
      : "This makes “" + safeText(plan.name) + "” available for new customer purchases again.";
    lifecycleDialog(action + " plan?", note, action + " plan", function (submit, feedback) {
      runPlanLifecycle(plan, retiring ? "/retire" : "/restore", "POST", submit, feedback, retiring ? "Plan retired. Existing subscriber access is unchanged." : "Plan restored for new purchases.");
    });
  }

  function openDeletePlanDialog(plan) {
    if (!canDeletePlans()) return;
    closePlanLifecycleDialog();
    var backdrop = document.createElement("div");
    var dialog = document.createElement("section");
    var heading = document.createElement("header");
    var title = document.createElement("h2");
    var note = document.createElement("p");
    var field = document.createElement("label");
    var input = document.createElement("input");
    var feedback = document.createElement("p");
    var footer = document.createElement("footer");
    var cancel = document.createElement("button");
    var submit = document.createElement("button");
    backdrop.className = "plan-dialog-backdrop";
    backdrop.id = "plan-lifecycle-dialog-backdrop";
    dialog.className = "plan-dialog plan-lifecycle-dialog";
    title.textContent = "Permanently delete plan?";
    note.className = "plan-dialog-note";
    note.textContent = "This cannot be undone. Deletion is allowed only when the plan has no subscriptions and no voucher codes. Type the exact plan name to confirm.";
    field.className = "plan-field";
    field.textContent = "Confirm plan name";
    input.type = "text";
    input.autocomplete = "off";
    input.setAttribute("aria-label", "Confirm plan name");
    field.appendChild(input);
    feedback.className = "plan-form-feedback";
    feedback.setAttribute("role", "alert");
    cancel.className = "button";
    cancel.type = "button";
    cancel.textContent = "Cancel";
    cancel.addEventListener("click", closePlanLifecycleDialog);
    submit.className = "button plan-delete";
    submit.type = "button";
    submit.textContent = "Delete permanently";
    submit.disabled = true;
    input.addEventListener("input", function () { submit.disabled = input.value !== plan.name; });
    submit.addEventListener("click", function () {
      if (input.value !== plan.name) return;
      runPlanLifecycle(plan, "", "DELETE", submit, feedback, "Plan permanently deleted.");
    });
    heading.append(title, note);
    footer.append(cancel, submit);
    dialog.append(heading, field, feedback, footer);
    backdrop.appendChild(dialog);
    backdrop.addEventListener("click", function (event) { if (event.target === backdrop) closePlanLifecycleDialog(); });
    document.body.appendChild(backdrop);
    input.focus();
  }

  function closePlanDialog() {
    var dialog = document.querySelector("#plan-dialog-backdrop");
    if (dialog) dialog.remove();
  }

  function showPlanMessage(message, error) {
    if (currentPage() !== "plans") return;
    var status = document.querySelector("#page-content .status");
    if (!status) return;
    status.classList.toggle("plan-status-error", Boolean(error));
    status.replaceChildren();
    status.textContent = message;
  }

  function onPageRendered(event) {
    if (event.detail !== "plans") return;
    if (loadedPlans) requestPlans(true); else requestPlans(false);
  }

  livePage.subscribe(function (page) { onPageRendered({ detail: page }); });
  if (currentPage() === "plans") onPageRendered({ detail: "plans" });
}());
