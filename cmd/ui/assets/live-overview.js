(function () {
  "use strict";
  var timer = 0;
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  function money(kobo) { return "₦" + (Number(kobo || 0) / 100).toLocaleString("en-NG", { minimumFractionDigits: 2, maximumFractionDigits: 2 }); }
  function bytes(value) {
    var amount = Number(value || 0), units = ["B", "KiB", "MiB", "GiB", "TiB"];
    if (!Number.isFinite(amount) || amount < 0) return "—";
    var unit = 0;
    while (amount >= 1024 && unit < units.length - 1) { amount /= 1024; unit += 1; }
    return amount.toLocaleString("en-NG", { maximumFractionDigits: unit ? 2 : 0 }) + " " + units[unit];
  }
  function freshSample(value) { return value && Date.now() - new Date(value).getTime() < 180000; }
  function showRouterTraffic(traffic) {
    var host = document.querySelector("[data-router-traffic]");
    if (!host) return;
    host.replaceChildren();
    if (!traffic || !Array.isArray(traffic.routers) || !traffic.routers.length) { host.textContent = "No managed routers are available."; return; }
    var table = document.createElement("table"); table.className = "data-table";
    var head = document.createElement("thead"), row = document.createElement("tr");
    ["Router", "HotSpot used", "WAN observed", "Active devices", "HotSpot hosts", "WAN sample"].forEach(function (label) { var cell = document.createElement("th"); cell.textContent = label; row.appendChild(cell); });
    head.appendChild(row); table.appendChild(head);
    var body = document.createElement("tbody");
    traffic.routers.forEach(function (router) {
      var tr = document.createElement("tr");
      var values = [router.name, bytes(router.hotspot_bytes), router.wan_sampled_at ? bytes(router.wan_bytes) : "—", String(router.active_devices || 0), freshSample(router.wan_sampled_at) ? String(router.observed_hosts || 0) : "—", router.wan_sampled_at ? new Date(router.wan_sampled_at).toLocaleString("en-NG") : "Awaiting sample"];
      values.forEach(function (value) { var cell = document.createElement("td"); cell.textContent = value; tr.appendChild(cell); });
      body.appendChild(tr);
    });
    table.appendChild(body); host.appendChild(table);
  }
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
      if (page === "overview" && data.traffic) {
        setMetric("HotSpot usage", bytes(data.traffic.hotspot_bytes));
        setMetric("HotSpot today", bytes(data.traffic.hotspot_today_bytes));
        setMetric("WAN observed", data.traffic.wan_available ? bytes(data.traffic.wan_bytes) : "—");
        setMetric("WAN today", data.traffic.wan_available ? bytes(data.traffic.wan_today_bytes) : "—");
        setMetric("Active devices", String(data.traffic.active_devices));
        var freshHosts = data.traffic.routers && data.traffic.routers.filter(function (router) { return freshSample(router.wan_sampled_at); });
        setMetric("Observed HotSpot hosts", freshHosts && freshHosts.length ? String(freshHosts.reduce(function (sum, router) { return sum + Number(router.observed_hosts || 0); }, 0)) : "—");
        showRouterTraffic(data.traffic);
      }
      setPageMetrics(page, data);
      var status = document.querySelector("#page-content .status span");
      if (status) status.textContent = "Live tenant data refreshed just now.";
      if (page === "overview") {
        return fetch(apiBase + "/api/v1/security/events?limit=4", { credentials: "same-origin", cache: "no-store" }).then(function (response) { if (!response.ok) throw new Error(); return response.json(); }).then(function (payload) { showActivity(payload.data); });
      }
    }).catch(function () { /* retain safe empty state */ });
  }
  window.addEventListener("netcore:page-rendered", function (event) {
    if (typeof clearInterval === "function") clearInterval(timer);
    if (["overview", "customers", "plans", "subscriptions"].indexOf(event.detail) !== -1) {
      load(event.detail);
      if (typeof setInterval === "function") timer = setInterval(function () { load(event.detail); }, 15000);
    }
  });
}());
