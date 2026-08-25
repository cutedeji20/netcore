(function () {
  "use strict";

  var loadedTransactions = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var livePage = window.NetCoreLivePage;
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
    var headings = ["Reference", "Customer", "Amount", "Recorded", "Status"];
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
      body.appendChild(row);
    });
    showState("records");
  }

  function requestTransactions(force) {
    if (requestInFlight || (loadedTransactions && !force)) return;
    requestInFlight = true;
    if (!loadedTransactions) showState("loading");
    fetch(apiBase + "/api/v1/billing/transactions?limit=25", {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) throw new Error("Billing request failed");
        return response.json();
      })
      .then(function (payload) {
        if (!payload || !Array.isArray(payload.data)) throw new Error("Billing response was invalid");
        loadedTransactions = payload.data;
        displayTransactions();
      })
      .catch(function () {
        // Last verified records remain visible when a refresh fails.
        if (loadedTransactions) displayTransactions();
        showState("error", { message: "Billing data could not be loaded. Please try again.", preserve: Boolean(loadedTransactions), retry: function () { requestTransactions(true); } });
      })
      .finally(function () {
        requestInFlight = false;
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "billing") return;
    if (loadedTransactions) requestTransactions(true);
    else requestTransactions();
  }

  livePage.subscribe(function (page) { onPageRendered({ detail: page }); });
  if (currentPage() === "billing") onPageRendered({ detail: "billing" });
}());
