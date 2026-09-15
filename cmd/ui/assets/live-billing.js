(function () {
  "use strict";

  var loadedTransactions = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var livePage = window.NetCoreLivePage;
  var listConfig = window.NetCoreLiveListConfig.get("billing");
  var listState = window.NetCoreLiveListControls.createState(listConfig.filters, listConfig.initialFilter, listConfig.filterParam);
  var loadedTransactionsMeta = {};
  var searchTimer = 0;
  var pendingQuery = "";
  var criteriaPending = false;
  var requestVersion = 0;
  var selectedPaymentIDs = new Set();
  var clearBusy = false;
  var currencyExponents = {
    JPY: 0, KRW: 0, VND: 0, CLP: 0, ISK: 0, XAF: 0, XOF: 0,
    BHD: 3, KWD: 3, OMR: 3, TND: 3, JOD: 3
  };

  function currentPage() {
    return livePage.current();
  }

  function showState(state, options) {
    livePage.showState("billing", state, options);
  }

  function safeText(value) {
    return value == null || value === "" ? "—" : String(value);
  }

  function formatDate(value) {
    if (!value) return "—";
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) return "—";
    return date.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }) + ", " + date.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
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

  function initials(customer) {
    var parts = [customer.first_name, customer.last_name].filter(Boolean);
    if (parts.length) {
      return parts.map(function (part) { return part.trim().slice(0, 1).toUpperCase(); }).join("").slice(0, 2);
    }
    return String(customer.customer_number || "?").slice(0, 2).toUpperCase();
  }

  function statusClass(item) {
    if (item.status === "SUCCESS" || item.status === "PAID") return "green";
    if (item.status === "PENDING" || item.status === "ISSUED") return "amber";
    if (item.status === "FAILED") return "red";
    return "gray";
  }

  function statusLabel(item) {
    if (item.source === "PAYMENT" && item.status === "SUCCESS") return "Verified";
    return safeText(item.status).toLowerCase().replace(/\b\w/g, function (letter) { return letter.toUpperCase(); });
  }

  function appendTextCell(row, value) {
    var cell = document.createElement("td");
    cell.textContent = safeText(value);
    row.appendChild(cell);
  }

  function appendStatusCell(row, item) {
    var cell = document.createElement("td");
    var label = document.createElement("span");
    label.className = "tag " + statusClass(item);
    label.textContent = statusLabel(item);
    cell.appendChild(label);
    row.appendChild(cell);
  }

  function isClearable(item) {
    return item.source === "PAYMENT" && (item.status === "PENDING" || item.status === "FAILED" || item.status === "ABANDONED");
  }

  function appendActionCell(row, item) {
    var cell = document.createElement("td");
    if (isClearable(item)) {
      var label = document.createElement("label");
      label.className = "billing-select";
      var input = document.createElement("input");
      input.type = "checkbox";
      input.checked = selectedPaymentIDs.has(item.id);
      input.setAttribute("aria-label", "Select payment attempt " + item.reference);
      input.addEventListener("change", function () {
        if (input.checked) selectedPaymentIDs.add(item.id);
        else selectedPaymentIDs.delete(item.id);
        renderClearControls();
      });
      label.append(input, document.createTextNode(" Select"));
      cell.appendChild(label);
    } else {
      cell.textContent = "—";
    }
    row.appendChild(cell);
  }

  function appendReferenceCell(row, item) {
    var cell = document.createElement("td");
    var entity = document.createElement("div");
    var mark = document.createElement("span");
    var copy = document.createElement("span");
    var title = document.createElement("strong");
    var detail = document.createElement("small");

    entity.className = "entity";
    mark.className = "entity-mark";
    mark.textContent = item.source === "PAYMENT" ? "₦" : "IN";
    title.textContent = safeText(item.reference);
    detail.textContent = item.source === "PAYMENT" ? "Payment" : "Invoice";
    copy.append(title, detail);
    entity.append(mark, copy);
    cell.appendChild(entity);
    row.appendChild(cell);
  }

  function appendCustomerCell(row, customer) {
    var cell = document.createElement("td");
    var entity = document.createElement("div");
    var mark = document.createElement("span");
    var copy = document.createElement("span");
    var name = document.createElement("strong");
    var detail = document.createElement("small");
    var fullName = [customer.first_name, customer.last_name].filter(Boolean).join(" ").trim() || customer.customer_number;

    entity.className = "entity";
    mark.className = "entity-mark";
    mark.textContent = initials(customer);
    name.textContent = safeText(fullName);
    detail.textContent = safeText(customer.customer_number);
    copy.append(name, detail);
    entity.append(mark, copy);
    cell.appendChild(entity);
    row.appendChild(cell);
  }

  function setHeadings(table) {
    var headings = ["Reference", "Customer", "Amount", "Recorded", "Status", "Action"];
    var headingRow = table.querySelector("thead tr");
    headingRow.replaceChildren();
    headings.forEach(function (value) {
      var heading = document.createElement("th");
      heading.textContent = value;
      headingRow.appendChild(heading);
    });
  }

  function displayTransactions() {
    if (!loadedTransactions || currentPage() !== "billing") return;
    var table = document.querySelector("#page-content .data-table");
    if (!table) return;

    setHeadings(table);
    var body = table.querySelector("tbody");
    body.replaceChildren();
    if (loadedTransactions.length === 0) {
      showState("empty", { message: "No billing transactions match this view." });
      return;
    }

    loadedTransactions.forEach(function (item) {
      var row = document.createElement("tr");
      appendReferenceCell(row, item);
      appendCustomerCell(row, item.customer || {});
      appendTextCell(row, formatPrice(item.amount_minor, item.currency));
      appendTextCell(row, formatDate(item.recorded_at));
      appendStatusCell(row, item);
      appendActionCell(row, item);
      body.appendChild(row);
    });
    showState("records");
  }

  function clearButton(label, scope, disabled) {
    var button = document.createElement("button");
    button.type = "button";
    button.className = "button payment-clear-button";
    button.textContent = label;
    button.disabled = disabled || clearBusy;
    button.addEventListener("click", function () { openClearDialog(scope); });
    return button;
  }

  function renderClearControls() {
    if (currentPage() !== "billing") return;
    var toolbar = document.querySelector("#page-content .toolbar");
    if (!toolbar) return;
    var existing = toolbar.querySelector(".payment-clear-actions");
    if (existing) existing.remove();
    var actions = document.createElement("div");
    actions.className = "payment-clear-actions";
    actions.append(
      clearButton("Clear selected", "SELECTED", selectedPaymentIDs.size === 0),
      clearButton("Clear pending", "PENDING", false),
      clearButton("Clear failed", "FAILED", false)
    );
    toolbar.appendChild(actions);
  }

  function dialogTitle(scope) {
    if (scope === "SELECTED") return "Clear selected payment attempts";
    return scope === "PENDING" ? "Clear all pending payment attempts" : "Clear all failed payment attempts";
  }

  function openClearDialog(scope) {
    if (clearBusy || (scope === "SELECTED" && selectedPaymentIDs.size === 0)) return;
    var backdrop = document.createElement("div");
    backdrop.className = "payment-clear-dialog-backdrop";
    var form = document.createElement("form");
    form.className = "payment-clear-dialog";
    var heading = document.createElement("h2"); heading.textContent = dialogTitle(scope);
    var note = document.createElement("p"); note.textContent = "This removes the selected non-successful attempts from normal billing history. Successful and refunded payments cannot be cleared.";
    var passwordLabel = document.createElement("label"); passwordLabel.textContent = "Current password";
    var password = document.createElement("input"); password.type = "password"; password.autocomplete = "current-password"; password.required = true; passwordLabel.appendChild(password);
    var mfaLabel = document.createElement("label"); mfaLabel.textContent = "Authenticator code";
    var mfa = document.createElement("input"); mfa.inputMode = "numeric"; mfa.autocomplete = "one-time-code"; mfa.required = true; mfaLabel.appendChild(mfa);
    var feedback = document.createElement("p"); feedback.className = "payment-clear-feedback";
    var footer = document.createElement("footer");
    var cancel = document.createElement("button"); cancel.type = "button"; cancel.className = "button"; cancel.textContent = "Cancel"; cancel.addEventListener("click", function () { backdrop.remove(); });
    var submit = document.createElement("button"); submit.type = "submit"; submit.className = "button primary"; submit.textContent = "Clear securely";
    footer.append(cancel, submit); form.append(heading, note, passwordLabel, mfaLabel, feedback, footer); backdrop.appendChild(form); document.body.appendChild(backdrop); password.focus();
    form.addEventListener("submit", function (event) {
      event.preventDefault();
      clearBusy = true; submit.disabled = true; feedback.textContent = "Clearing payment attempts…"; renderClearControls();
      fetch(apiBase + "/api/v1/billing/payment-attempts/clear", { method: "POST", credentials: "include", cache: "no-store", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ scope: scope, payment_ids: scope === "SELECTED" ? Array.from(selectedPaymentIDs) : [], password: password.value, mfa_code: mfa.value }) })
        .then(function (response) { return response.json().catch(function () { return {}; }).then(function (body) { if (!response.ok) throw new Error((body.error && body.error.message) || "Payment attempts could not be cleared."); return body; }); })
        .then(function (body) { selectedPaymentIDs.clear(); backdrop.remove(); if (window.NetCoreToast) window.NetCoreToast.show((body.cleared || 0) + " payment attempt(s) cleared."); requestTransactions(true); })
        .catch(function (error) { feedback.textContent = error.message || "Payment attempts could not be cleared."; })
        .finally(function () { clearBusy = false; submit.disabled = false; renderClearControls(); });
    });
  }

  function filterOptions() {
    return listConfig.filters.map(function (filter) { return { value: filter, label: filter || "All" }; });
  }

  function renderControls() {
    if (currentPage() !== "billing") return;
    livePage.renderListControls("billing", {
      query: criteriaPending ? pendingQuery : listState.query, filter: listState.filter, filters: filterOptions(), busy: requestInFlight,
      hasPrevious: listState.previousCursors.length > 0, hasNext: listState.hasMore,
      searchPlaceholder: "Search billing", searchLabel: "Search billing", filterLabel: "Filter billing",
      onSearch: function (query) {
        clearTimeout(searchTimer);
        pendingQuery = query;
        criteriaPending = true;
        requestVersion += 1;
        window.NetCoreLiveListControls.applyCriteria(listState, query, listState.filter);
        searchTimer = setTimeout(function () { criteriaPending = false; requestVersion += 1; requestTransactions(true); }, 250);
      },
      onFilter: function (filter) { applyCriteria(listState.query, filter); },
      onNext: function () { if (criteriaPending) return; if (window.NetCoreLiveListControls.nextPage(listState)) requestTransactions(true); else renderControls(); },
      onPrevious: function () { if (criteriaPending) return; if (listState.previousCursors.length) { window.NetCoreLiveListControls.previousPage(listState); requestTransactions(true); } else renderControls(); }
    });
  }

  function applyCriteria(query, filter) {
    if (window.NetCoreLiveListControls.applyCriteria(listState, query, filter)) requestTransactions(true);
  }

  function requestTransactions(force) {
    if (requestInFlight || (loadedTransactions && !force)) return;
    requestInFlight = true;
    var requestVersionAtStart = requestVersion;
    renderControls();
    renderClearControls();
    if (!loadedTransactions) showState("loading");
    fetch(window.NetCoreLiveListControls.requestURL(apiBase, listConfig.endpoint, listState, 25), {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) throw new Error("Billing request failed");
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) throw new Error("Billing response was invalid");
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        loadedTransactions = payload.data;
        loadedTransactionsMeta = payload.meta || {};
        window.NetCoreLiveListControls.applyResponseMeta(listState, loadedTransactionsMeta);
        displayTransactions();
        renderClearControls();
      })
      .catch(function () {
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        // Last verified records remain visible when a refresh fails.
        if (loadedTransactions) displayTransactions();
        showState("error", { message: "Billing data could not be loaded. Please try again.", preserve: Boolean(loadedTransactions), retry: function () { requestTransactions(true); } });
      })
      .finally(function () {
        requestInFlight = false;
        renderControls();
        if (!criteriaPending && requestVersionAtStart !== requestVersion) requestTransactions(true);
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "billing") return;
    renderControls();
    if (loadedTransactions) requestTransactions(true);
    else requestTransactions();
  }

  livePage.subscribe(function (page) { onPageRendered({ detail: page }); });
}());
