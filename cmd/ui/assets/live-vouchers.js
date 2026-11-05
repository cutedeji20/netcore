(function () {
  "use strict";

  var loadedBatches = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var livePage = window.NetCoreLivePage;
  var listConfig = window.NetCoreLiveListConfig.get("vouchers");
  var listState = window.NetCoreLiveListControls.createState(listConfig.filters, listConfig.initialFilter, listConfig.filterParam);
  var loadedBatchesMeta = {};
  var searchTimer = 0;
  var pendingQuery = "";
  var criteriaPending = false;
  var requestVersion = 0;
  var summaryPeriod = "30d";
  var summaryInFlight = false;
  function canWrite() { var p = window.NETCORE_PRINCIPAL || {}; return Array.isArray(p.permissions) && p.permissions.indexOf("voucher.write") !== -1; }
  function apiError(response) { return response.json().catch(function () { return {}; }).then(function (body) { throw new Error(body && body.error && body.error.message || "Voucher action failed."); }); }

  function currentPage() {
    return livePage.current();
  }

  function showState(state, options) {
    livePage.showState("vouchers", state, options);
  }

  function safeText(value) {
    return value == null || value === "" ? "—" : String(value);
  }

  function periodLabel(period) {
    return ({ today: "Today", "7d": "Last 7 days", "30d": "Last 30 days", this_month: "This month", last_month: "Last month", all: "All time" })[period] || "Selected period";
  }

  function formatNumber(value) {
    var number = Number(value);
    return Number.isFinite(number) ? new Intl.NumberFormat("en-NG").format(number) : "—";
  }

  function formatRate(basisPoints) {
    var number = Number(basisPoints);
    return Number.isFinite(number) ? (number / 100).toFixed(number % 100 === 0 ? 0 : 1) + "%" : "—";
  }

  function formatMoney(minor, currency) {
    var number = Number(minor);
    if (!Number.isFinite(number) || !currency) return "—";
    try { return new Intl.NumberFormat("en-NG", { style: "currency", currency: String(currency), maximumFractionDigits: 2 }).format(number / 100); } catch (_) { return formatNumber(number) + " " + currency; }
  }

  function formatDate(value) {
    if (!value) return "No expiry";
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) return "—";
    return "Expires " + date.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
  }

  function generatedParts(value) {
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) return { date: "—", time: "—", iso: "" };
    return {
      date: date.toLocaleDateString("en-NG", { timeZone: "Africa/Lagos", year: "numeric", month: "short", day: "numeric" }),
      time: date.toLocaleTimeString("en-NG", { timeZone: "Africa/Lagos", hour: "numeric", minute: "2-digit", hour12: true }),
      iso: date.toISOString()
    };
  }

  function statusClass(status) {
    if (status === "ACTIVE") return "green";
    if (status === "MIXED") return "amber";
    if (status === "EXPIRED") return "amber";
    return "gray";
  }

  function statusLabel(status) {
    return safeText(status).toLowerCase().replace(/\b\w/g, function (letter) { return letter.toUpperCase(); });
  }

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

  function appendBatchCell(row, batch) {
    var cell = document.createElement("td");
    var entity = document.createElement("div");
    var mark = document.createElement("span");
    var copy = document.createElement("span");
    var name = document.createElement("strong");
    var detail = document.createElement("small");
    var generated = document.createElement("small");
    var generatedAt = generatedParts(batch.created_at);

    entity.className = "entity";
    mark.className = "entity-mark";
    mark.textContent = "VC";
    name.textContent = "Batch " + safeText(String(batch.id || "").slice(0, 8));
    detail.textContent = formatDate(batch.expires_at);
    generated.className = "voucher-generated-inline";
    generated.textContent = "Generated " + generatedAt.date + " · " + generatedAt.time + " WAT";
    copy.append(name, detail, generated);
    entity.append(mark, copy);
    cell.appendChild(entity);
    row.appendChild(cell);
  }

  function appendPlanCell(row, batch) {
    var cell = document.createElement("td");
    var entity = document.createElement("div");
    var copy = document.createElement("span");
    var name = document.createElement("strong");
    var detail = document.createElement("small");

    entity.className = "entity";
    name.textContent = safeText(batch.plan_name);
    detail.textContent = safeText(batch.available) + " available";
    copy.append(name, detail);
    entity.append(copy);
    cell.appendChild(entity);
    row.appendChild(cell);
  }

  function appendOwnerCell(row, batch) {
    var cell = document.createElement("td");
    var entity = document.createElement("div");
    var copy = document.createElement("span");
    var name = document.createElement("strong");
    var detail = document.createElement("small");
    name.textContent = safeText(batch.owner_display);
    detail.textContent = batch.owner_type === "CUSTOMER" ? "Purchased by customer" : batch.owner_type === "STAFF" ? "Issued by staff" : "Owner unavailable";
    entity.className = "entity"; copy.append(name, detail); entity.append(copy); cell.append(entity); row.appendChild(cell);
  }

  function appendGeneratedCell(row, createdAt) {
    var parts = generatedParts(createdAt);
    var cell = document.createElement("td");
    var stamp = document.createElement("time");
    var date = document.createElement("strong");
    var clock = document.createElement("small");
    cell.className = "voucher-generated-cell";
    if (parts.iso) stamp.dateTime = parts.iso;
    date.textContent = parts.date;
    clock.textContent = parts.time + " WAT";
    stamp.append(date, clock);
    cell.appendChild(stamp);
    row.appendChild(cell);
  }

  function setHeadings(table) {
    var headings = ["Batch", "Access bundle", "Owner", "Generated", "Issued", "Redeemed", "Status"];
    if (canWrite()) headings.push("Actions");
    var headingRow = table.querySelector("thead tr");
    headingRow.replaceChildren();
    headings.forEach(function (value) {
      var heading = document.createElement("th");
      heading.textContent = value;
      headingRow.appendChild(heading);
    });
  }

  function displayBatches() {
    if (!loadedBatches || currentPage() !== "vouchers") return;
    var table = document.querySelector("#page-content .data-table");
    if (!table) return;

    setHeadings(table);
    var body = table.querySelector("tbody");
    body.replaceChildren();
    if (loadedBatches.length === 0) {
      showState("empty", { message: "No voucher batches match this view." });
      return;
    }

    loadedBatches.forEach(function (batch) {
      var row = document.createElement("tr");
      appendBatchCell(row, batch);
      appendPlanCell(row, batch);
      appendOwnerCell(row, batch);
      appendGeneratedCell(row, batch.created_at);
      appendTextCell(row, batch.issued);
      appendTextCell(row, batch.redeemed);
      appendStatusCell(row, batch.status);
      if (canWrite()) {
        var actionCell = document.createElement("td"), reveal = document.createElement("button");
        reveal.type = "button"; reveal.className = "button"; reveal.textContent = "Manage codes";
        reveal.addEventListener("click", function () { openManageDialog(batch); });
        actionCell.append(reveal); row.append(actionCell);
      }
      body.appendChild(row);
    });
    showState("records");
  }

  function renderControls() {
    if (currentPage() !== "vouchers") return;
    livePage.renderListControls("vouchers", {
      query: criteriaPending ? pendingQuery : listState.query, filter: listState.filter, filters: filterOptions(), busy: requestInFlight,
      hasPrevious: listState.previousCursors.length > 0, hasNext: listState.hasMore,
      searchPlaceholder: "Search batch, plan, or customer", searchLabel: "Search voucher batches or customers", filterLabel: "Filter voucher status",
      onSearch: function (query) {
        clearTimeout(searchTimer);
        pendingQuery = query;
        criteriaPending = true;
        requestVersion += 1;
        window.NetCoreLiveListControls.applyCriteria(listState, query, listState.filter);
        searchTimer = setTimeout(function () { criteriaPending = false; requestVersion += 1; requestBatches(true); }, 250);
      },
	  onFilter: function (filter) { applyCriteria(listState.query, filter); },
      onNext: function () { if (criteriaPending) return; if (window.NetCoreLiveListControls.nextPage(listState)) requestBatches(true); else renderControls(); },
      onPrevious: function () { if (criteriaPending) return; if (listState.previousCursors.length) { window.NetCoreLiveListControls.previousPage(listState); requestBatches(true); } else renderControls(); }
    });
  }

  function renderSummaryControls() {
    if (currentPage() !== "vouchers") return;
    var heading = document.querySelector("#page-content .page-heading .heading-actions");
    if (!heading || heading.querySelector(".voucher-summary-period")) return;
    var label = document.createElement("label");
    var select = document.createElement("select");
    label.className = "voucher-summary-range";
    label.textContent = "Statistics period";
    select.className = "voucher-summary-period";
    ["today", "7d", "30d", "this_month", "last_month", "all"].forEach(function (period) {
      var option = document.createElement("option");
      option.value = period; option.textContent = periodLabel(period); option.selected = period === summaryPeriod;
      select.append(option);
    });
    select.addEventListener("change", function () { summaryPeriod = select.value; requestSummary(); });
    label.append(select); heading.prepend(label);
  }

  function summaryMetric(card, label, value, detail, state) {
    if (!card) return;
    var metricLabel = card.querySelector(".metric-label");
    var metricValue = card.querySelector(".metric-value");
    var metricChange = card.querySelector(".metric-change");
    if (metricLabel) metricLabel.textContent = label;
    if (metricValue) metricValue.textContent = value;
    if (metricChange) { metricChange.textContent = detail; metricChange.className = "metric-change" + (state ? " " + state : ""); }
  }

  function renderSummary(payload) {
    if (currentPage() !== "vouchers" || !payload || typeof payload !== "object") return;
    var summary = payload.summary;
    if (!summary || typeof summary !== "object") return;
    var cards = document.querySelectorAll("#page-content .metric-grid .metric");
    var period = periodLabel(summary.period || summaryPeriod);
    summaryMetric(cards[0], "Vouchers issued", formatNumber(summary.issued), period, "");
    summaryMetric(cards[1], "Redeemed", formatNumber(summary.redeemed), "Redeemed in " + period.toLowerCase(), "");
    summaryMetric(cards[2], "Redemption rate", formatRate(payload.redemption_rate_basis_points), "Of vouchers issued in " + period.toLowerCase(), "");
    summaryMetric(cards[3], "Expired unused", formatNumber(summary.expired_unused), "Expired in " + period.toLowerCase(), summary.expired_unused > 0 ? "warn" : "");

    var detailList = document.querySelector("#page-content .detail-list");
    if (!detailList) return;
    var detail = [
      ["Available now", formatNumber(summary.available)],
      ["Queued redemptions", formatNumber(summary.queued_redemptions)],
      ["Customer-purchased batches", formatNumber(summary.customer_batches)],
      ["Staff-issued batches", formatNumber(summary.staff_batches)],
      ["Paid voucher orders", formatNumber(summary.paid_orders)],
      ["Voucher sales collected", formatMoney(summary.collected_minor, summary.currency)],
      ["Reseller discounts", formatMoney(summary.discount_minor, summary.currency)],
      ["Revoked vouchers", formatNumber(summary.revoked)]
    ];
    detailList.replaceChildren();
    detail.forEach(function (row) { var item = document.createElement("li"), name = document.createElement("span"), value = document.createElement("strong"); name.textContent = row[0]; value.textContent = row[1]; item.append(name, value); detailList.append(item); });
    detailList.dataset.voucherSummaryLoaded = "true";
  }

  function hasSummaryHost() {
    return Boolean(document.querySelector("#page-content .metric-grid") && document.querySelector("#page-content .detail-list"));
  }

  function requestSummary() {
    if (summaryInFlight || currentPage() !== "vouchers" || !hasSummaryHost()) return;
    summaryInFlight = true;
    fetch(apiBase + "/api/v1/vouchers/summary?period=" + encodeURIComponent(summaryPeriod), { credentials: "include", cache: "no-store" })
      .then(function (response) { if (!response.ok) throw new Error("Voucher summary request failed"); return response.json(); })
      .then(renderSummary)
      .catch(function () {
        var detailList = document.querySelector("#page-content .detail-list");
        if (detailList && !detailList.dataset.voucherSummaryLoaded) detailList.firstElementChild && (detailList.firstElementChild.textContent = "Voucher statistics could not be loaded.");
      })
      .finally(function () { summaryInFlight = false; });
  }

  function filterOptions() {
    return listConfig.filters.map(function (filter) { return { value: filter, label: filter ? statusLabel(filter) : "All statuses" }; });
  }

  function applyCriteria(query, filter) {
    if (window.NetCoreLiveListControls.applyCriteria(listState, query, filter)) requestBatches(true);
  }

  function requestBatches(force) {
    if (requestInFlight || (loadedBatches && !force)) return;
    requestInFlight = true;
    var requestVersionAtStart = requestVersion;
    renderControls();
    if (!loadedBatches) showState("loading");
    fetch(window.NetCoreLiveListControls.requestURL(apiBase, listConfig.endpoint, listState, 25), {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) throw new Error("Vouchers request failed");
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) throw new Error("Vouchers response was invalid");
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        loadedBatches = payload.data;
        loadedBatchesMeta = payload.meta || {};
        window.NetCoreLiveListControls.applyResponseMeta(listState, loadedBatchesMeta);
        displayBatches();
      })
      .catch(function () {
        if (criteriaPending || requestVersionAtStart !== requestVersion) return;
        // Last verified records remain visible when a refresh fails.
        if (loadedBatches) displayBatches();
        showState("error", { message: "Vouchers could not be loaded. Please try again.", preserve: Boolean(loadedBatches), retry: function () { requestBatches(true); } });
      })
      .finally(function () {
        requestInFlight = false;
        renderControls();
        if (!criteriaPending && requestVersionAtStart !== requestVersion) requestBatches(true);
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "vouchers") return;
    renderStaffControls();
    renderSummaryControls();
    renderControls();
    requestSummary();
    if (loadedBatches) requestBatches(true);
    else requestBatches();
  }

  var dialogKeyHandler = null;
  function closeVoucherDialog() {
    var backdrop = document.querySelector("#voucher-dialog-backdrop");
    if (backdrop) backdrop.remove();
    if (dialogKeyHandler) document.removeEventListener("keydown", dialogKeyHandler);
    dialogKeyHandler = null;
  }

  function openVoucherDialog(titleText) {
    closeVoucherDialog();
    var backdrop = document.createElement("div"), dialog = document.createElement("section"), heading = document.createElement("header"), title = document.createElement("h2"), close = document.createElement("button");
    backdrop.id = "voucher-dialog-backdrop"; backdrop.className = "voucher-dialog-backdrop";
    dialog.className = "voucher-dialog"; dialog.setAttribute("role", "dialog"); dialog.setAttribute("aria-modal", "true"); dialog.setAttribute("aria-labelledby", "voucher-dialog-title");
    title.id = "voucher-dialog-title"; title.textContent = titleText;
    close.type = "button"; close.className = "button"; close.textContent = "Close"; close.addEventListener("click", closeVoucherDialog);
    heading.append(title, close); dialog.append(heading); backdrop.append(dialog); document.body.append(backdrop);
    backdrop.addEventListener("click", function (event) { if (event.target === backdrop) closeVoucherDialog(); });
    dialogKeyHandler = function (event) { if (event.key === "Escape") closeVoucherDialog(); };
    document.addEventListener("keydown", dialogKeyHandler);
    return dialog;
  }

  function voucherField(labelText, input) {
    var label = document.createElement("label"); label.className = "voucher-field"; label.textContent = labelText; label.append(input); return label;
  }

  function renderStaffControls() {
    if (!canWrite()) return;
    var heading = document.querySelector("#page-content .page-heading");
    if (!heading || heading.querySelector(".voucher-issue-control")) return;
    var actions = heading.querySelector(".heading-actions");
    if (!actions) { actions = document.createElement("div"); actions.className = "heading-actions"; heading.append(actions); }
    var issue = document.createElement("button"); issue.type = "button"; issue.className = "button primary voucher-issue-control"; issue.textContent = "Issue vouchers";
    issue.addEventListener("click", openIssueDialog); actions.append(issue);
  }

  function openIssueDialog() {
    var dialog = openVoucherDialog("Issue prepaid vouchers"), form = document.createElement("form"), feedback = document.createElement("p"), footer = document.createElement("footer");
    var plan = document.createElement("select"); plan.required = true;
    var quantity = document.createElement("input"); quantity.type = "number"; quantity.min = "1"; quantity.max = "100"; quantity.value = "1"; quantity.required = true;
    var expiry = document.createElement("input"); expiry.type = "datetime-local";
    var reason = document.createElement("input"); reason.type = "text"; reason.maxLength = 500; reason.required = true;
    var password = document.createElement("input"); password.type = "password"; password.autocomplete = "current-password"; password.required = true;
    var mfa = document.createElement("input"); mfa.type = "text"; mfa.inputMode = "numeric"; mfa.maxLength = 6; mfa.required = true;
    var submit = document.createElement("button"); submit.type = "submit"; submit.className = "button primary"; submit.textContent = "Issue vouchers";
    form.className = "voucher-form"; feedback.className = "voucher-feedback"; feedback.setAttribute("role", "status");
    [["Plan",plan],["Quantity",quantity],["Expires before use (optional)",expiry],["Reason",reason],["Current password",password],["Authenticator code",mfa]].forEach(function (field) { form.append(voucherField(field[0],field[1])); });
    footer.append(submit); form.append(feedback,footer); dialog.append(form); plan.focus();
    fetch(apiBase + "/api/v1/plans?limit=100&status=ACTIVE", { credentials:"include", cache:"no-store" }).then(function (r) { if (!r.ok) return apiError(r); return r.json(); }).then(function (body) {
      (Array.isArray(body.data) ? body.data : []).forEach(function (item) { var option=document.createElement("option"); option.value=String(item.id || ""); option.textContent=String(item.name || "Internet plan"); plan.append(option); });
      if (!plan.options.length) feedback.textContent="No active plans are available.";
    }).catch(function (error) { feedback.textContent=error.message || "Plans could not be loaded."; });
    form.addEventListener("submit", function (event) {
      event.preventDefault(); var count=Number(quantity.value);
      if (!plan.value || !Number.isInteger(count) || count<1 || count>100 || !reason.value.trim()) { feedback.textContent="Choose a plan, 1–100 vouchers, and a reason."; return; }
      submit.disabled=true; feedback.textContent="Issuing encrypted vouchers…";
      fetch(apiBase + "/api/v1/vouchers/batches", { method:"POST", credentials:"include", headers:{"Content-Type":"application/json"}, body:JSON.stringify({ plan_id:plan.value, quantity:count, expires_at:expiry.value ? new Date(expiry.value).toISOString() : null, reason:reason.value.trim(), password:password.value, mfa_code:mfa.value }) })
        .then(function (r) { if (!r.ok) return apiError(r); return r.json(); })
        .then(function () { feedback.textContent="Vouchers issued. Use Manage codes to retrieve them."; password.value=""; mfa.value=""; requestBatches(true); })
        .catch(function (error) { feedback.textContent=error.message || "Voucher issuance failed."; }).finally(function () { submit.disabled=false; });
    });
  }

  function openManageDialog(batch) {
    var dialog = openVoucherDialog("Manage batch " + safeText(String(batch.id || "").slice(0,8))), form = document.createElement("form"), submit = document.createElement("button"), print = document.createElement("button"), result = document.createElement("div"), footer = document.createElement("footer");
    submit.type="submit"; submit.className="button primary"; submit.textContent="Reveal codes";
	print.type="button"; print.className="button"; print.textContent="Print PDF (20 per page)";
    form.className="voucher-form"; result.className="voucher-codes"; result.setAttribute("role","status");
	footer.append(submit,print); form.append(footer); dialog.append(form,result); submit.focus();
	print.addEventListener("click", function () {
	  print.disabled=true; result.textContent="Preparing printable vouchers…";
	  fetch(apiBase + "/api/v1/vouchers/batches/" + encodeURIComponent(batch.id) + "/print", { method:"POST", credentials:"include", headers:{"Content-Type":"application/json","Accept":"application/pdf"}, body:"{}" })
	    .then(function (response) { if (!response.ok) return apiError(response); return response.blob(); })
	    .then(function (blob) { var url=URL.createObjectURL(blob), link=document.createElement("a"); link.href=url; link.download="voucher-batch-"+String(batch.id || "").slice(0,8)+".pdf"; document.body.append(link); link.click(); link.remove(); window.setTimeout(function () { URL.revokeObjectURL(url); },1000); result.textContent="Printable PDF downloaded."; })
	    .catch(function (error) { result.textContent=error.message || "Printable vouchers could not be created."; }).finally(function () { print.disabled=false; });
	});
    form.addEventListener("submit", function (event) {
      event.preventDefault(); submit.disabled=true; result.textContent="Checking access…";
      fetch(apiBase + "/api/v1/vouchers/batches/" + encodeURIComponent(batch.id) + "/reveal", { method:"POST", credentials:"include", headers:{"Content-Type":"application/json"}, body:"{}" })
        .then(function (r) { if (!r.ok) return apiError(r); return r.json(); })
        .then(function (body) {
          result.replaceChildren(); (Array.isArray(body.data) ? body.data : []).forEach(function (item) {
            var line=document.createElement("div"), code=document.createElement("code"), revoke=document.createElement("button");
            line.className="voucher-code-row"; code.textContent=String(item.code || ""); revoke.type="button"; revoke.className="button"; revoke.textContent="Revoke";
            revoke.addEventListener("click", function () { if (!window.confirm("Revoke this voucher? Active access may need an operator disconnect.")) return; var password=window.prompt("Current password required to revoke this voucher."); if (password===null) return; var mfa=window.prompt("Authenticator code required to revoke this voucher."); if (mfa===null) return; revoke.disabled=true; fetch(apiBase + "/api/v1/vouchers/" + encodeURIComponent(item.voucher_id) + "/revoke", { method:"POST", credentials:"include", headers:{"Content-Type":"application/json"}, body:JSON.stringify({ password:password, mfa_code:mfa }) }).then(function (r) { if (!r.ok) return apiError(r); code.textContent="Revoked"; requestBatches(true); }).catch(function (error) { result.textContent=error.message || "Revocation failed."; }).finally(function () { revoke.disabled=false; }); });
            line.append(code,revoke); result.append(line);
          });
        }).catch(function (error) { result.textContent=error.message || "Codes could not be retrieved."; }).finally(function () { submit.disabled=false; });
    });
  }

  livePage.subscribe(function (page) { onPageRendered({ detail: page }); });
}());
