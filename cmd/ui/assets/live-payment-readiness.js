(function () {
  "use strict";

  var payload = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");

  function currentPage() {
    return window.location.hash.slice(1) || "overview";
  }

  function toneClass(tone) {
    if (tone === "ready") return "green";
    if (tone === "disabled" || tone === "attention") return "amber";
    return "gray";
  }

  function displayReadiness() {
    if (!payload || currentPage() !== "billing" || !window.NetCorePaymentReadiness) return;
    var content = document.querySelector("#page-content");
    var split = content.querySelector(".split-grid");
    if (!split) return;
    var existing = content.querySelector("#payment-readiness");
    if (existing) existing.remove();

    var view = window.NetCorePaymentReadiness.toDisplay(payload);
    var panel = document.createElement("section");
    panel.id = "payment-readiness";
    panel.className = "panel payment-readiness";

    var header = document.createElement("div");
    header.className = "panel-header";
    var heading = document.createElement("div");
    var title = document.createElement("h2");
    var description = document.createElement("p");
    var badge = document.createElement("span");
    title.textContent = "Payment operations";
    description.textContent = "Server-side readiness only. Secret values are never displayed here.";
    badge.className = "tag " + toneClass(view.tone);
    badge.textContent = view.title;
    heading.append(title, description);
    header.append(heading, badge);

    var list = document.createElement("ul");
    list.className = "detail-list";
    view.rows.forEach(function (row) {
      var item = document.createElement("li");
      var label = document.createElement("span");
      var value = document.createElement("strong");
      label.textContent = row[0];
      value.textContent = row[1];
      item.append(label, value);
      list.appendChild(item);
    });
    panel.append(header, list);
    content.insertBefore(panel, split);
  }

  function requestReadiness() {
    if (payload || requestInFlight) return;
    requestInFlight = true;
    fetch(apiBase + "/api/v1/payments/readiness", {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) return null;
        return response.json();
      })
      .then(function (body) {
        if (!body || typeof body.provider !== "string" || typeof body.checkout_status !== "string") return;
        payload = body;
        displayReadiness();
      })
      .catch(function () {
        // A failed or unauthorised request never turns into an invented status.
      })
      .finally(function () {
        requestInFlight = false;
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "billing") return;
    if (payload) displayReadiness();
    else requestReadiness();
  }

  window.addEventListener("netcore:page-rendered", onPageRendered);
  if (currentPage() === "billing") onPageRendered({ detail: "billing" });
}());
