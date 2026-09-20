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
  function load() {
    if (location.hash.slice(1) && location.hash.slice(1) !== "overview") return;
    Promise.all([fetch(apiBase + "/api/v1/operations/overview", { credentials: "same-origin", cache: "no-store" }), fetch(apiBase + "/api/v1/security/events?limit=4", { credentials: "same-origin", cache: "no-store" })]).then(function (responses) { if (!responses[0].ok || !responses[1].ok) throw new Error(); return Promise.all([responses[0].json(), responses[1].json()]); }).then(function (payload) {
      var data = payload[0];
      setMetric("Active customers", String(data.active_customers));
      setMetric("Online sessions", String(data.online_sessions));
      setMetric("Collected today", money(data.collected_today_minor));
      setMetric("Needs attention", String(data.attention));
      var status = document.querySelector("#page-content .status span");
      if (status) status.textContent = "Live tenant data refreshed just now.";
      showActivity(payload[1].data);
    }).catch(function () { /* retain safe empty state */ });
  }
  window.addEventListener("netcore:page-rendered", function (event) {
    clearInterval(timer);
    if (event.detail === "overview") { load(); timer = setInterval(load, 15000); }
  });
}());
