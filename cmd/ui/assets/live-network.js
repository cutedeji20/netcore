(function () {
  "use strict";

  var loadedRouters = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var livePage = window.NetCoreLivePage;
  var listConfig = window.NetCoreLiveListConfig.get("network");
  var listState = window.NetCoreLiveListControls.createState(listConfig.filters, listConfig.initialFilter, listConfig.filterParam);
  var loadedRoutersMeta = {};
  var searchTimer = 0;
  var pendingQuery = "";
  var criteriaPending = false;
  var requestVersion = 0;

  function currentPage() {
    return livePage.current();
  }

  function showState(state, options) {
    livePage.showState("network", state, options);
  }

  function safeText(value) {
    return value == null || value === "" ? "—" : String(value);
  }

  function canWriteNetwork() {
    var permissions = window.NETCORE_PRINCIPAL && window.NETCORE_PRINCIPAL.permissions;
    return Array.isArray(permissions) && permissions.indexOf("network.write") !== -1;
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

  function actionButton(label, className, listener) {
    var button = document.createElement("button");
    button.className = "button " + className;
    button.type = "button";
    button.textContent = label;
    button.addEventListener("click", listener);
    return button;
  }

  function appendActionCell(row, router) {
    var cell = document.createElement("td");
    cell.className = "network-actions";
    cell.appendChild(actionButton("Configure AAA", "network-aaa", function () { openAAADialog(router); }));
    row.appendChild(cell);
  }

  function setHeadings(table) {
    var headings = ["Location", "Router", "AAA", "Last seen", "Status"];
    if (canWriteNetwork()) headings.push("Action");
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
      if (canWriteNetwork()) appendActionCell(row, router);
      body.appendChild(row);
    });
    showState("records");
  }

  function filterOptions() {
    return listConfig.filters.map(function (filter) { return { value: filter, label: filter || "All" }; });
  }

  function addCreateButton() {
    var heading = document.querySelector("#page-content .page-heading");
    if (!heading || !canWriteNetwork() || heading.querySelector(".network-create")) return;
    var actions = document.createElement("div");
    var create = actionButton("＋ Add router", "primary network-create", openRouterDialog);
    actions.className = "heading-actions";
    actions.appendChild(create);
    heading.appendChild(actions);
  }

  function closeNetworkDialog() {
    var dialog = document.querySelector("#network-dialog-backdrop");
    if (dialog) dialog.remove();
  }

  function createField(labelText, name, type, required) {
    var field = document.createElement("label");
    var input = document.createElement("input");
    field.className = "plan-field";
    field.textContent = labelText;
    input.name = name;
    input.type = type || "text";
    input.required = required !== false;
    input.autocomplete = type === "password" ? "current-password" : "off";
    field.appendChild(input);
    return field;
  }

  function requestError(response, fallback) {
    return response.json().catch(function () { return {}; }).then(function (body) {
      if (!response.ok) throw new Error(body && body.error && body.error.message ? body.error.message : fallback);
      return body;
    });
  }

  function openRouterDialog() {
    if (!canWriteNetwork()) return;
    closeNetworkDialog();
    var backdrop = document.createElement("div");
    var dialog = document.createElement("section");
    var heading = document.createElement("header");
    var title = document.createElement("h2");
    var note = document.createElement("p");
    var form = document.createElement("form");
    var feedback = document.createElement("p");
    var footer = document.createElement("footer");
    var cancel = actionButton("Cancel", "", closeNetworkDialog);
    var submit = actionButton("Add router", "primary", function () {});
    backdrop.className = "plan-dialog-backdrop";
    backdrop.id = "network-dialog-backdrop";
    dialog.className = "plan-dialog network-dialog";
    title.textContent = "Add router";
    note.className = "plan-dialog-note";
    note.textContent = "Add private management and RADIUS source addresses. AAA starts disabled until the router has been configured and tested.";
    form.className = "plan-form";
    form.noValidate = true;
    form.append(
      createField("Router name", "name"),
      createField("Management IP", "management_ip"),
      createField("NAS IP address", "nas_ip_address"),
      createField("RADIUS source IP", "radius_source_ip"),
      createField("Your password", "password", "password"),
      createField("Authenticator code", "mfa_code", "text")
    );
    feedback.className = "plan-form-feedback";
    feedback.setAttribute("role", "alert");
    submit.type = "submit";
    footer.append(cancel, submit);
    form.append(feedback, footer);
    form.addEventListener("submit", function (event) {
      event.preventDefault();
      var data = new FormData(form);
      var payload = {};
      ["name", "management_ip", "nas_ip_address", "radius_source_ip", "password", "mfa_code"].forEach(function (key) { payload[key] = String(data.get(key) || "").trim(); });
      submit.disabled = true;
      feedback.textContent = "Adding router…";
      fetch(apiBase + "/api/v1/network/routers", { method: "POST", credentials: "include", cache: "no-store", headers: { "Content-Type": "application/json", "Accept": "application/json" }, body: JSON.stringify(payload) })
        .then(function (response) { return requestError(response, "The router could not be added."); })
        .then(function () { closeNetworkDialog(); requestRouters(true); })
        .catch(function (error) { feedback.textContent = error.message || "The router could not be added."; })
        .finally(function () { submit.disabled = false; });
    });
    heading.append(title, note);
    dialog.append(heading, form);
    backdrop.appendChild(dialog);
    backdrop.addEventListener("click", function (event) { if (event.target === backdrop) closeNetworkDialog(); });
    document.body.appendChild(backdrop);
    form.elements.name.focus();
  }

  function openAAADialog(router) {
    if (!canWriteNetwork()) return;
    closeNetworkDialog();
    var backdrop = document.createElement("div");
    var dialog = document.createElement("section");
    var heading = document.createElement("header");
    var title = document.createElement("h2");
    var note = document.createElement("p");
    var form = document.createElement("form");
    var feedback = document.createElement("p");
    var footer = document.createElement("footer");
    var cancel = actionButton("Close", "", closeNetworkDialog);
    var download = actionButton("Download setup", "primary", function () {});
    var status = router.aaa_status === "ACTIVE" ? "disable" : "activate";
    var verified = router.aaa_verified === true;
    var confirmation = document.createElement("label");
    var confirmed = document.createElement("input");
    var verify = actionButton("Record private test", "", function () {});
    var change = actionButton(status === "activate" ? "Activate AAA" : "Disable AAA", status === "disable" ? "plan-delete" : "", function () {});
    backdrop.className = "plan-dialog-backdrop";
    backdrop.id = "network-dialog-backdrop";
    dialog.className = "plan-dialog network-dialog";
    title.textContent = "Configure AAA · " + safeText(router.name);
    note.className = "plan-dialog-note";
    note.textContent = "Download setup rotates the shared secret. Apply the file over the private management path, then record a successful Access-Request and accounting test before activation.";
    form.className = "plan-form";
    form.noValidate = true;
    form.append(createField("Your password", "password", "password"), createField("Authenticator code", "mfa_code", "text"));
    confirmation.className = "plan-field";
    confirmed.type = "checkbox";
    confirmed.name = "private_test_confirmed";
    confirmation.append(confirmed, document.createTextNode(" I completed the private Access-Request and accounting test."));
    verify.disabled = true;
    confirmed.addEventListener("change", function () { verify.disabled = !confirmed.checked; });
    feedback.className = "plan-form-feedback";
    feedback.setAttribute("role", "alert");
    function stepUpPayload() {
      var data = new FormData(form);
      return { password: String(data.get("password") || ""), mfa_code: String(data.get("mfa_code") || "").trim() };
    }
    download.addEventListener("click", function () {
      var payload = stepUpPayload();
      download.disabled = true;
      feedback.textContent = "Preparing one-time setup file…";
      fetch(apiBase + "/api/v1/network/routers/" + encodeURIComponent(router.id) + "/aaa/export", { method: "POST", credentials: "include", cache: "no-store", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) })
        .then(function (response) { if (!response.ok) return requestError(response, "The setup file could not be prepared."); return response.blob(); })
        .then(function (blob) {
          if (!(blob instanceof Blob)) return;
          var downloadURL = URL.createObjectURL(blob);
          var link = document.createElement("a");
          link.href = downloadURL;
          link.download = "netcore-router-aaa-" + encodeURIComponent(router.id) + ".txt";
          link.click();
          setTimeout(function () { URL.revokeObjectURL(downloadURL); }, 0);
          feedback.textContent = "Setup file downloaded. Keep it private and apply it now.";
        })
        .catch(function (error) { feedback.textContent = error.message || "The setup file could not be prepared."; })
        .finally(function () { download.disabled = false; });
    });
    verify.addEventListener("click", function () {
      var payload = stepUpPayload();
      payload.private_test_confirmed = confirmed.checked;
      verify.disabled = true;
      feedback.textContent = "Recording private test…";
      fetch(apiBase + "/api/v1/network/routers/" + encodeURIComponent(router.id) + "/aaa/verify", { method: "POST", credentials: "include", cache: "no-store", headers: { "Content-Type": "application/json", "Accept": "application/json" }, body: JSON.stringify(payload) })
        .then(function (response) { return requestError(response, "The private test could not be recorded."); })
        .then(function () { closeNetworkDialog(); requestRouters(true); })
        .catch(function (error) { feedback.textContent = error.message || "The private test could not be recorded."; })
        .finally(function () { verify.disabled = !confirmed.checked; });
    });
    change.addEventListener("click", function () {
      var payload = stepUpPayload();
      change.disabled = true;
      feedback.textContent = status === "activate" ? "Activating AAA…" : "Disabling AAA…";
      fetch(apiBase + "/api/v1/network/routers/" + encodeURIComponent(router.id) + "/aaa/" + status, { method: "POST", credentials: "include", cache: "no-store", headers: { "Content-Type": "application/json", "Accept": "application/json" }, body: JSON.stringify(payload) })
        .then(function (response) { return requestError(response, "AAA status could not be changed."); })
        .then(function () { closeNetworkDialog(); requestRouters(true); })
        .catch(function (error) { feedback.textContent = error.message || "AAA status could not be changed."; })
        .finally(function () { change.disabled = false; });
    });
    heading.append(title, note);
    footer.append(cancel, download);
    if (router.aaa_status === "ACTIVE" || verified) {
      footer.append(change);
    } else {
      form.append(confirmation);
      footer.append(verify);
    }
    form.append(feedback, footer);
    dialog.append(heading, form);
    backdrop.appendChild(dialog);
    backdrop.addEventListener("click", function (event) { if (event.target === backdrop) closeNetworkDialog(); });
    document.body.appendChild(backdrop);
    form.elements.password.focus();
  }

  function renderControls() {
    if (currentPage() !== "network") return;
    livePage.renderListControls("network", {
      query: criteriaPending ? pendingQuery : listState.query, filter: listState.filter, filters: filterOptions(), busy: requestInFlight,
      hasPrevious: listState.previousCursors.length > 0, hasNext: listState.hasMore,
      searchPlaceholder: "Search routers", searchLabel: "Search routers", filterLabel: "Filter routers",
      onSearch: function (query) {
        clearTimeout(searchTimer);
        pendingQuery = query;
        criteriaPending = true;
        requestVersion += 1;
        window.NetCoreLiveListControls.applyCriteria(listState, query, listState.filter);
        searchTimer = setTimeout(function () { criteriaPending = false; requestVersion += 1; requestRouters(true); }, 250);
      },
      onFilter: function (filter) { applyCriteria(listState.query, filter); },
      onNext: function () { if (criteriaPending) return; if (window.NetCoreLiveListControls.nextPage(listState)) requestRouters(true); else renderControls(); },
      onPrevious: function () { if (criteriaPending) return; if (listState.previousCursors.length) { window.NetCoreLiveListControls.previousPage(listState); requestRouters(true); } else renderControls(); }
    });
  }

  function applyCriteria(query, filter) {
    if (window.NetCoreLiveListControls.applyCriteria(listState, query, filter)) requestRouters(true);
  }

  function requestRouters(force) {
    if (requestInFlight || (loadedRouters && !force)) return;
    requestInFlight = true;
    var requestVersionAtStart = requestVersion;
    renderControls();
    if (!loadedRouters) showState("loading");
    fetch(window.NetCoreLiveListControls.requestURL(apiBase, listConfig.endpoint, listState, 25), {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) throw new Error("Network request failed");
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) throw new Error("Network response was invalid");
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        loadedRouters = payload.data;
        loadedRoutersMeta = payload.meta || {};
        window.NetCoreLiveListControls.applyResponseMeta(listState, loadedRoutersMeta);
        displayRouters();
      })
      .catch(function () {
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        // Last verified records remain visible when a refresh fails.
        if (loadedRouters) displayRouters();
        showState("error", { message: "Network data could not be loaded. Please try again.", preserve: Boolean(loadedRouters), retry: function () { requestRouters(true); } });
      })
      .finally(function () {
        requestInFlight = false;
        renderControls();
        if (!criteriaPending && requestVersionAtStart !== requestVersion) requestRouters(true);
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "network") return;
    renderControls();
    if (loadedRouters) requestRouters(true);
    else requestRouters();
  }

  livePage.subscribe(function (page) { onPageRendered({ detail: page }); });
}());
