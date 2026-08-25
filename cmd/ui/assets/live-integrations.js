(function () {
  "use strict";
  var apiBase = String(window.NETCORE_API_URL || window.location.origin).replace(/\/$/, "");
  var payload = null;
  var requestInFlight = false;
  var livePage = window.NetCoreLivePage;
  var activePage = livePage.current();

  function insertPanel(content, panel) {
    var splitGrid = content.querySelector(".split-grid");
    content.insertBefore(panel, splitGrid && splitGrid.parentNode === content ? splitGrid : null);
  }

  function renderState(state, retry) {
    if (activePage !== "settings") return;
    var content = document.querySelector("#page-content");
    if (!content) return;
    var existing = content.querySelector("#integration-settings");
    if (existing) existing.remove();
    var panel = element("section"); panel.id = "integration-settings"; panel.className = "panel"; panel.setAttribute("data-live-state", state);
    var message = element("p", state === "loading" ? "Loading integrations…" : state === "empty" ? "No integrations are available for this workspace." : "Integrations could not be loaded. Please try again."); message.className = "description"; message.setAttribute("role", "status"); panel.appendChild(message);
    if (state === "error") { var button = element("button", "Retry"); button.type = "button"; button.className = "button"; button.addEventListener("click", retry); panel.appendChild(button); }
    insertPanel(content, panel);
  }

  function appendFailure() {
    var panel = document.querySelector("#integration-settings");
    if (!panel) return;
    var message = element("p", "Integrations could not be loaded. Last verified records are still shown."); message.className = "description"; message.setAttribute("role", "status");
    var button = element("button", "Retry"); button.type = "button"; button.className = "button"; button.addEventListener("click", function () { request(true); });
    panel.append(message, button);
  }

  function request(force) {
    if (requestInFlight || (payload && !force)) { if (payload) render(); return Promise.resolve(); }
    requestInFlight = true;
    if (!payload) renderState("loading");
    return fetch(apiBase + "/api/v1/integrations", { credentials: "include", cache: "no-store" })
      .then(function (response) { if (!response.ok) throw new Error("Integrations request failed"); return response.json(); })
      .then(function (body) { if (!body || !Array.isArray(body.integrations)) throw new Error("Integrations response was invalid"); payload = body.integrations; render(); })
      .catch(function () { if (payload) { render(); appendFailure(); } else renderState("error", function () { request(true); }); })
      .finally(function () { requestInFlight = false; });
  }
  function element(name, text) { var node = document.createElement(name); if (text != null) node.textContent = text; return node; }
  function render() {
    if (activePage !== "settings" || !window.NetCoreIntegrationDisplay) return;
    var content = document.querySelector("#page-content");
    if (!content) return;
    var existing = content.querySelector("#integration-settings");
    if (existing) existing.remove();
    var panel = element("section"); panel.id = "integration-settings"; panel.className = "panel";
    var header = element("div"); header.className = "panel-header";
    var heading = element("div"); heading.append(element("h2", "Integrations"), element("p", "Connect provider accounts securely. Saved keys are never displayed."));
    header.appendChild(heading); panel.appendChild(header);
    var cards = element("div"); cards.className = "integration-cards";
    var records = window.NetCoreIntegrationDisplay.toCards(payload);
    panel.setAttribute("data-live-state", records.length ? "records" : "empty");
    if (!records.length) cards.appendChild(element("p", "No integrations are available for this workspace."));
    records.forEach(function (card) {
      var item = element("article"); item.className = "integration-card";
      var copy = element("div"); copy.append(element("strong", card.name), element("p", card.detail));
      var state = element("span", card.status); state.className = "tag " + (card.status === "Active" ? "green" : card.status === "Disabled" ? "amber" : "gray");
      var action = element("button", card.action); action.type = "button"; action.className = "button primary"; action.addEventListener("click", function () { openForm(card.provider); });
      item.append(copy, state, action); cards.appendChild(item);
	  if (card.status === "Active") { var disable = element("button", "Disable"); disable.type = "button"; disable.className = "button"; disable.addEventListener("click", function () { openConfirmation(card.provider, "disable"); }); var disconnect = element("button", "Disconnect"); disconnect.type = "button"; disconnect.className = "button"; disconnect.addEventListener("click", function () { openConfirmation(card.provider, "disconnect"); }); item.append(disable, disconnect); }
    });
    panel.appendChild(cards); insertPanel(content, panel);
  }
  function openForm(provider) {
    var overlay = element("div"); overlay.className = "integration-dialog";
    var dialog = element("form"); dialog.className = "panel";
    dialog.append(element("h2", provider === "resend" ? "Connect Resend" : "Connect Paystack"), element("p", provider === "resend" ? "Confirm your password and authenticator code. A verification email will be sent to your administrator address before this sender is activated." : "Confirm your password and authenticator code. NetCore will perform a read-only Paystack balance check before this key is activated."));
    function field(label, type, name) { var wrap = element("label", label); var input = element("input"); input.type = type; input.name = name; input.required = true; input.autocomplete = "off"; wrap.appendChild(input); dialog.appendChild(wrap); return input; }
    var credential = field(provider === "resend" ? "Resend API key" : "Paystack secret key", "password", "credential");
    var sender; var mode;
    if (provider === "resend") sender = field("Verified sender (for example, DataHub <hotspot@durabledatahubs.com>)", "text", "sender_email");
    else { var select = element("label", "Mode"); mode = element("select"); mode.name = "mode"; ["TEST", "LIVE"].forEach(function (value) { var option = element("option", value); option.value = value; mode.appendChild(option); }); select.appendChild(mode); dialog.appendChild(select); }
    var password = field("Current password", "password", "password"); var mfa = field("Authenticator code", "text", "mfa_code"); mfa.inputMode = "numeric"; mfa.maxLength = 6;
    var message = element("p"); message.setAttribute("role", "status");
    var actions = element("div"); actions.className = "heading-actions"; var cancel = element("button", "Cancel"); cancel.type = "button"; cancel.className = "button"; cancel.addEventListener("click", close); var submit = element("button", "Save securely"); submit.type = "submit"; submit.className = "button primary"; actions.append(cancel, submit); dialog.append(message, actions); overlay.appendChild(dialog); document.body.appendChild(overlay); credential.focus();
    function close() { dialog.reset(); overlay.remove(); }
    dialog.addEventListener("submit", function (event) { event.preventDefault(); var body = { credential: credential.value, password: password.value, mfa_code: mfa.value }; if (sender) body.sender_email = sender.value; if (mode) body.mode = mode.value; dialog.reset(); submit.disabled = true; fetch(apiBase + "/api/v1/integrations/" + provider, { method: "PUT", credentials: "include", cache: "no-store", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }).then(function (response) { if (!response.ok) throw new Error(); close(); return request(true); }).catch(function () { message.textContent = "The configuration was not saved. Check your password, authenticator code, and settings."; submit.disabled = false; }); });
  }

  function openConfirmation(provider, action) {
	var overlay = element("div"); overlay.className = "integration-dialog"; var dialog = element("form"); dialog.className = "panel";
	dialog.append(element("h2", action === "disconnect" ? "Disconnect " + provider : "Disable " + provider), element("p", action === "disconnect" ? "This removes the encrypted credential and stops the integration." : "This stops the integration immediately. You can reconnect it later."));
	function field(label, type) { var wrap = element("label", label), input = element("input"); input.type = type; input.required = true; input.autocomplete = "off"; wrap.appendChild(input); dialog.appendChild(wrap); return input; }
	var password = field("Current password", "password"), mfa = field("Authenticator code", "text"), message = element("p"); mfa.inputMode = "numeric"; mfa.maxLength = 6;
	var actions = element("div"); actions.className = "heading-actions"; var cancel = element("button", "Cancel"); cancel.type = "button"; cancel.className = "button"; cancel.addEventListener("click", close); var submit = element("button", action === "disconnect" ? "Disconnect" : "Disable"); submit.type = "submit"; submit.className = "button primary"; actions.append(cancel, submit); dialog.append(message, actions); overlay.appendChild(dialog); document.body.appendChild(overlay); password.focus();
	function close() { dialog.reset(); overlay.remove(); }
	dialog.addEventListener("submit", function (event) { event.preventDefault(); var body = { password: password.value, mfa_code: mfa.value }; dialog.reset(); submit.disabled = true; fetch(apiBase + "/api/v1/integrations/" + provider + (action === "disable" ? "/disable" : ""), { method: action === "disable" ? "POST" : "DELETE", credentials: "include", cache: "no-store", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }).then(function (response) { if (!response.ok) throw new Error(); close(); return request(true); }).catch(function () { message.textContent = "The change was not completed. Check your password and authenticator code."; submit.disabled = false; }); });
  }
  livePage.subscribe(function (page) {
    activePage = page;
    if (activePage === "settings") request(true);
  });
  if (activePage === "settings") request();
}());
