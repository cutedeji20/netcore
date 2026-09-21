(function () {
  "use strict";
  var timer = 0;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  function money(kobo) { return "₦" + (Number(kobo || 0) / 100).toLocaleString("en-NG", { minimumFractionDigits: 2, maximumFractionDigits: 2 }); }
  function setMetric(label, value) {
    document.querySelectorAll("#page-content .metric").forEach(function (card) {
      if (card.querySelector(".metric-label") && card.querySelector(".metric-label").textContent === label) card.querySelector(".metric-value").textContent = value;
    });
  }
  function lifetime(seconds) { var days = Math.round(Number(seconds || 0) / 86400); return days ? days + " days" : "—"; }
  function setPageMetrics(page, data) {
    if (!data) return;
    if (page === "customers" && data.customers) {
      setMetric("Active customers", data.customers.active); setMetric("New this month", data.customers.new_this_month); setMetric("Needs review", data.customers.needs_review); setMetric("Support queue", data.customers.without_active_plan);
    }
    if (page === "plans" && data.plans) {
      setMetric("Published plans", data.plans.published); setMetric("Most selected", data.plans.most_selected); setMetric("Highest growth", data.plans.highest_growth); setMetric("Draft changes", data.plans.retired);
    }
    if (page === "subscriptions" && data.subscriptions) {
      setMetric("Active", data.subscriptions.active); setMetric("Renewing this week", data.subscriptions.renewing_this_week); setMetric("On hold", data.subscriptions.on_hold); setMetric("Average lifetime", lifetime(data.subscriptions.average_lifetime_seconds));
    }
  }
  function text(value) { return String(value || ""); }
  function showActivity(events) {
    var list = document.querySelector("[data-overview-activity]");
    if (!list) return;
    list.replaceChildren();
    if (!Array.isArray(events) || !events.length) {
      var empty = document.createElement("li");
      empty.textContent = "No authorised activity yet.";
      list.appendChild(empty);
      return;
    }
    events.slice(0, 4).forEach(function (event) {
      var item = document.createElement("li"), body = document.createElement("div"), title = document.createElement("strong"), detail = document.createElement("span"), when = document.createElement("time");
      title.textContent = text(event.action).replaceAll("_", " ");
      detail.textContent = [text(event.actor), text(event.resource_type)].filter(Boolean).join(" · ");
      when.textContent = event.created_at ? new Date(event.created_at).toLocaleString("en-NG") : "";
      body.append(title, detail, when); item.appendChild(body); list.appendChild(item);
    });
  }
  function load(page) {
    if (["overview", "customers", "plans", "subscriptions"].indexOf(page) === -1) return;
    fetch(apiBase + "/api/v1/operations/overview", { credentials: "same-origin", cache: "no-store" }).then(function (response) { if (!response.ok) throw new Error(); return response.json(); }).then(function (data) {
      setMetric("Active customers", String(data.active_customers));
      setMetric("Online sessions", String(data.online_sessions));
      setMetric("Collected today", money(data.collected_today_minor));
      setMetric("Needs attention", String(data.attention));
      setPageMetrics(page, data);
      var status = document.querySelector("#page-content .status span");
      if (status) status.textContent = "Live tenant data refreshed just now.";
      if (page === "overview") {
        return fetch(apiBase + "/api/v1/security/events?limit=4", { credentials: "same-origin", cache: "no-store" }).then(function (response) { if (!response.ok) throw new Error(); return response.json(); }).then(function (payload) { showActivity(payload.data); });
      }
    }).catch(function () { /* retain safe empty state */ });
  }
  window.addEventListener("netcore:page-rendered", function (event) {
    clearInterval(timer);
    if (["overview", "customers", "plans", "subscriptions"].indexOf(event.detail) !== -1) { load(event.detail); timer = setInterval(function () { load(event.detail); }, 15000); }
  });
}());
