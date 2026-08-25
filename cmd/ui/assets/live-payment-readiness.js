(function () {
  "use strict";

  var payload = null;
  var requestInFlight = false;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var livePage = window.NetCoreLivePage;

  function currentPage() {
    return livePage.current();
  }

  function showState(state, retry) {
    if (currentPage() !== "billing") return;
    var content = document.querySelector("#page-content");
    var split = content && content.querySelector(".split-grid");
    if (!split) return;
    var existing = content.querySelector("#payment-readiness");
    if (existing) existing.remove();
    var panel = document.createElement("section");
    var message = document.createElement("p");
    panel.id = "payment-readiness";
    panel.className = "panel payment-readiness";
    panel.setAttribute("data-live-state", state);
    message.className = "description";
    message.setAttribute("role", "status");
    message.textContent = state === "loading" ? "Loading payment readiness…" : state === "empty" ? "No payment readiness details are available." : "Payment readiness could not be loaded. Please try again.";
    panel.appendChild(message);
    if (state === "error") {
      var button = document.createElement("button");
      button.type = "button";
      button.className = "button";
      button.textContent = "Retry";
      button.addEventListener("click", retry);
      panel.appendChild(button);
    }
    content.insertBefore(panel, split);
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
    if (!Array.isArray(view.rows) || view.rows.length === 0) {
      showState("empty");
      return;
    }
    var panel = document.createElement("section");
    panel.id = "payment-readiness";
    panel.className = "panel payment-readiness";
    panel.setAttribute("data-live-state", "records");

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

  function showRefreshError() {
    var panel = document.querySelector("#payment-readiness");
    if (!panel) return;
    var existing = panel.querySelector(".live-refresh-error");
    if (existing) existing.remove();
    var message = document.createElement("p");
    var button = document.createElement("button");
    message.className = "description live-refresh-error";
    message.setAttribute("role", "status");
    message.textContent = "Payment readiness could not be refreshed. Verified details are still shown.";
    button.type = "button";
    button.className = "button live-refresh-error";
    button.textContent = "Retry";
    button.addEventListener("click", function () { requestReadiness(true); });
    panel.append(message, button);
  }

  function requestReadiness(force) {
    if ((payload && !force) || requestInFlight) return;
    requestInFlight = true;
    showState("loading");
    fetch(apiBase + "/api/v1/payments/readiness", {
      credentials: "include",
      cache: "no-store"
    })
      .then(function (response) {
        if (!response.ok) throw new Error("Payment readiness request failed");
        return response.json();
      })
      .then(function (body) {
        if (!body || typeof body.provider !== "string" || typeof body.checkout_status !== "string") throw new Error("Payment readiness response was invalid");
        payload = body;
        displayReadiness();
      })
      .catch(function () {
        // Last verified records remain visible when a refresh fails; no status is invented.
        if (payload) {
          displayReadiness();
          showRefreshError();
        }
        else showState("error", function () { requestReadiness(true); });
      })
      .finally(function () {
        requestInFlight = false;
      });
  }

  function onPageRendered(event) {
    if (event.detail !== "billing") return;
    if (payload) requestReadiness(true);
    else requestReadiness();
  }

  livePage.subscribe(function (page) { onPageRendered({ detail: page }); });
  if (currentPage() === "billing") onPageRendered({ detail: "billing" });
}());
