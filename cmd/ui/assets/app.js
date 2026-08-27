const pages = {
  overview: { title: "Good afternoon, Amara", kicker: "Tuesday, 12 August · Lagos Hub", description: "Here is the clearest view of today’s customers, revenue and network operations.", action: "Create customer" },
  customers: { title: "Customers", kicker: "Customer workspace", description: "Find, support and manage each subscriber across their account and connection history.", action: "Create customer", cols: ["Customer", "Plan", "Connection", "Balance", "Status"], rows: [["CN|Chika Nwosu|CUS-10482 · +234 803 111 4280", "Home Pro 50", "Online · 3h 14m", "₦0", "Active|green"], ["BL|BrightWorks Ltd.|CUS-10476 · Enterprise account", "Business 100", "Online · 6 devices", "₦0", "Active|green"], ["AS|Amina Sule|CUS-10470 · +234 802 310 9012", "Home 20", "Offline · 1d", "₦12,500", "Due soon|amber"], ["DE|Duro Ejike|CUS-10465 · +234 806 880 3919", "Home Pro 50", "Online · 28m", "₦0", "Active|green"]], side: [["Active customers", "2,846"], ["New this month", "126"], ["Needs review", "12"], ["Support queue", "8"]] },
  subscriptions: { title: "Subscriptions", kicker: "Service management", description: "Track active access, upcoming renewals and accounts that need attention.", action: "New subscription", cols: ["Subscriber", "Plan", "Renews", "Service", "Action"], rows: [["AS|Amina Sule|SUB-88092", "Home 20", "Tomorrow", "Payment pending|amber", "Send reminder"], ["CW|Cedar Works|SUB-88080", "Business 100", "14 Aug", "Active|green", "View"], ["GC|Glenwood Court|SUB-88068", "Estate 200", "15 Aug", "Review|amber", "View"]], side: [["Active", "2,692"], ["Renewing this week", "184"], ["On hold", "27"], ["Average lifetime", "8.4 mo"]] },
  plans: { title: "Plans & pricing", kicker: "Catalog", description: "Create the internet products your teams can sell and your network can enforce.", action: "Create plan", cols: ["Plan", "Speed", "Price", "Subscribers"], rows: [["20|Home 20|30 days · 500 GB", "20 / 10 Mbps", "₦12,500", "1,184"], ["50|Home Pro 50|30 days · 1 TB", "50 / 25 Mbps", "₦21,000", "986"], ["100|Business 100|30 days · unlimited", "100 / 50 Mbps", "₦85,000", "198"], ["200|Estate 200|30 days · unlimited", "200 / 100 Mbps", "₦180,000", "44"]], side: [["Published plans", "4"], ["Most selected", "Home 20"], ["Highest growth", "Pro 50"], ["Draft changes", "2"]] },
  network: { title: "Network & AAA", kicker: "Operations center", description: "Keep routers, RADIUS and access points within a single, calm operating view.", action: "Add network device", cols: ["Location", "Router", "AAA", "Latency", "Status"], rows: [["VI|Victoria Island POP|10.10.1.0/24", "RB5009-VI-01", "Responding", "9 ms", "Healthy|green"], ["YA|Yaba POP|10.10.2.0/24", "CCR2004-YB-01", "Responding", "86 ms", "Review|amber"], ["LE|Lekki Phase 1|10.10.3.0/24", "RB5009-LK-01", "Responding", "13 ms", "Healthy|green"]], side: [["Access requests today", "12,842"], ["Successful authentication", "99.6%"], ["Accounting updates", "71,280"], ["Unknown NAS", "0"]] },
  sessions: { title: "Sessions & usage", kicker: "Live access", description: "Inspect current connections, bandwidth use and quota status without leaving the workspace.", action: "Find session", cols: ["Customer", "Router", "Started", "Usage", "Service"], rows: [["CN|Chika Nwosu|10.10.3.42", "Lekki 01", "3h 14m", "14.8 GB / 1 TB", "Normal|green"], ["DE|Duro Ejike|10.10.2.88", "Yaba 01", "28m", "0.6 GB / 1 TB", "Normal|green"], ["GC|Glenwood Court|10.10.1.101", "VI 01", "1d 4h", "4.2 TB / Unlimited", "High use|amber"]], side: [["Online now", "1,932"], ["Data transferred", "18.4 TB"], ["Peak sessions", "2,114"], ["Quota alerts", "8"]] },
  billing: { title: "Invoices & payments", kicker: "Revenue operations", description: "Review money received, outstanding invoices and any payment that needs investigation.", action: "Create invoice", cols: ["Reference", "Customer", "Amount", "Received", "Status"], rows: [["PAY-749202", "BrightWorks Ltd.", "₦85,000", "Today, 13:18", "Verified|green"], ["INV-220114", "Amina Sule", "₦12,500", "Due tomorrow", "Open|amber"], ["PAY-749196", "Duro Ejike", "₦21,000", "Today, 12:46", "Verified|green"], ["PAY-749188", "Glenwood Court", "₦180,000", "Today, 11:34", "Review|red"]], side: [["Collected this month", "₦26.8m"], ["Open invoices", "₦4.2m"], ["Payment success", "98.7%"], ["Needs review", "3"]] },
  vouchers: { title: "Vouchers", kicker: "Prepaid access", description: "Create and distribute short-lived access bundles for retail and hotspot customers.", action: "Create voucher batch", cols: ["Batch", "Access bundle", "Issued", "Redeemed", "Status"], rows: [["Campus-Aug-01", "24 hours · 10 GB", "1,000", "861", "Active|green"], ["Marina-weekend", "72 hours · 25 GB", "500", "206", "Active|green"], ["Launch-event", "6 hours · 2 GB", "300", "300", "Completed|gray"]], side: [["Codes redeemed", "2,104"], ["Average redemption", "72%"], ["Expired unused", "83"], ["Suspicious attempts", "0"]] },
  team: { title: "Team & roles", kicker: "Access control", description: "Give each colleague only the access they need to safely do their work.", action: "Invite teammate", cols: ["Team member", "Role", "Last active", "MFA", "Access"], rows: [["AO|Amara Okafor|amara@lagoshub.test", "Operations lead", "Now", "Enabled|green", "Full|indigo"], ["IE|Ifeanyi Eze|ifeanyi@lagoshub.test", "Support specialist", "16 min ago", "Enabled|green", "Support|gray"], ["MO|Mariam Ojo|mariam@lagoshub.test", "Billing officer", "1 h ago", "Set up|amber", "Billing|gray"]], side: [["Operations leads", "1"], ["Support specialists", "4"], ["Billing officers", "2"], ["MFA coverage", "7 / 8"]] },
  security: { title: "Security center", kicker: "Protection status", description: "Review sign-in activity, MFA coverage and controls that protect customer accounts.", action: "Review activity", cols: ["Time", "Event", "Actor", "Source", "Outcome"], rows: [["13:08", "New administrator session", "Amara Okafor", "Lagos · recognised device", "Allowed|green"], ["11:24", "MFA verification", "Ifeanyi Eze", "Support action", "Verified|green"], ["09:17", "Rate limit protected sign-in", "Unknown", "Public endpoint", "Blocked|amber"]], side: [["Privileged MFA", "7 / 8"], ["Active sessions", "14"], ["Failed sign-ins, 24h", "9"], ["Secret references exposed", "0"]] },
  automations: { title: "Automations", kicker: "Workflows", description: "Turn repeatable operations into dependable, visible workflows.", action: "Create automation", cols: ["Workflow", "Trigger", "Next run", "Owner", "Status"], rows: [["Renewal reminders", "24 hours before renewal", "Today, 18:00", "Customer success", "Ready|green"], ["Payment reconciliation", "Every 10 minutes", "In 4 minutes", "Billing", "Ready|green"], ["Usage exception review", "Every hour", "In 39 minutes", "Operations", "Draft|amber"]], side: [["Ready workflows", "2"], ["Draft workflows", "1"], ["Runs today", "158"], ["Exceptions", "0"]] },
  settings: { title: "Workspace settings", kicker: "Configuration", description: "Set up your organisation, payment rules and integrations before switching to live operations.", action: "Save changes", cols: ["Setup item", "State", "Owner", "Last updated"], rows: [["Organisation profile", "Complete|green", "Amara Okafor", "Today"], ["Payment provider", "Not connected|amber", "Billing", "Never"], ["Secret store", "Needs adapter|amber", "Platform", "Today"], ["Network devices", "18 registered|green", "Operations", "38 sec ago"]], side: [["Workspace", "Lagos Hub"], ["Timezone", "Africa/Lagos"], ["Currency", "NGN"], ["API mode", "Preview data"]] },
};

const icons = { overview:"⌂", customers:"◉", subscriptions:"↗", plans:"◇", network:"⌁", sessions:"◌", billing:"₦", vouchers:"▣", team:"♙", security:"⟡", automations:"⇄", settings:"⚙" };
const content = document.querySelector("#page-content");
const navItems = [...document.querySelectorAll("[data-page]")];
const sidebar = document.querySelector("#sidebar");
const modal = document.querySelector("#command-modal");
const commandInput = document.querySelector("#command-input");
const commandOptions = document.querySelector("#command-options");
const toast = document.querySelector("#toast");
const appShell = document.querySelector("#admin-shell");
const accessScreen = document.querySelector("#admin-access");
const logoutButton = document.querySelector("#logout-button");
const adminConfig = window.NETCORE_ADMIN_CONFIG || {};
const liveAdapterPaths = [
  "/live-page.js",
  "/live-list-config.js",
  "/live-list-controls.js",
  "/live-customers.js", "/live-subscriptions.js", "/live-plans.js", "/live-sessions.js",
  "/live-billing.js", "/live-network.js", "/live-vouchers.js", "/live-team.js",
  "/live-security.js", "/live-automations.js", "/live-workspace.js", "/live-payment-readiness.js", "/integration-display.js", "/live-integrations.js", "/session-expiry.js"
];
const hasLiveConfig = adminConfig.mode === "live" && /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(String(adminConfig.tenant || ""));
let adminState = { authorised: false, adaptersLoaded: false, identity: null };
let cancelSessionExpiry = () => {};
let toastTimer;
const readOnlyOperationalPages = new Set(["subscriptions", "sessions", "vouchers", "network", "billing", "security", "automations", "settings"]);

const tag = (item) => {
  const [label, type = "gray"] = item.split("|");
  return `<span class="tag ${type}">${label}</span>`;
};
const entity = (item) => {
  const [initials, name, detail] = item.split("|");
  return `<div class="entity"><span class="entity-mark">${initials}</span><span><strong>${name}</strong><small>${detail}</small></span></div>`;
};
const isTag = (value) => value.includes("|") && ["green","indigo","amber","red","gray"].includes(value.split("|")[1]);
const heading = (page, id) => {
  const status = id === "settings" ? "Operational controls appear below" : readOnlyOperationalPages.has(id) ? "Read-only operational view" : "";
  return `<div class="page-heading"><div><p class="kicker">${page.kicker}</p><h1>${page.title}</h1><p class="description">${page.description}</p></div><div class="heading-actions">${status ? `<p class="description" role="status">${status}</p>` : ""}</div></div>`;
};
const metrics = (items) => `<section class="metric-grid">${items.map(([name, value], index) => `<article class="metric"><p class="metric-label">${name}</p><p class="metric-value">${value}</p><span class="metric-change ${index === 2 ? "warn" : ""}">${index === 0 ? "↑ Healthy trend" : index === 1 ? "Updated today" : index === 2 ? "Needs attention" : "In current view"}</span></article>`).join("")}</section>`;
const table = (page) => `<section class="panel table"><div class="toolbar"><input aria-label="Search ${page.title}" placeholder="Search ${page.title.toLowerCase()}" /><button class="button" type="button" data-toast="Filters will appear here.">All records ▾</button></div><table class="data-table"><thead><tr>${page.cols.map((column) => `<th>${column}</th>`).join("")}</tr></thead><tbody>${page.rows.map((row) => `<tr>${row.map((cell, index) => `<td>${index === 0 && cell.split("|").length === 3 ? entity(cell) : isTag(cell) ? tag(cell) : cell}</td>`).join("")}</tr>`).join("")}</tbody></table></section>`;

function dashboard() {
  return `${heading(pages.overview)}
    <div class="status"><i></i><strong>Operations are healthy</strong><span>All essential services are responding normally.</span><span class="updated">Updated just now</span></div>
    ${metrics([["Active customers","2,846"],["Online sessions","1,932"],["Collected today","₦1.84m"],["Needs attention","12"]])}
    <section class="dashboard-grid">
      <article class="panel"><div class="panel-header"><div><h2>Collection trend</h2><p>Verified payments across the last seven days</p></div><button class="panel-link" data-page="billing">View payments →</button></div><div class="chart"><div class="chart-labels"><span>₦2m</span><span>₦1.5m</span><span>₦1m</span><span>₦500k</span><span>₦0</span></div><div class="chart-area"><div class="chart-grid"><span></span><span></span><span></span><span></span><span></span></div><div class="chart-line"><div class="bars"><i style="height:36%"></i><i style="height:48%"></i><i style="height:43%"></i><i style="height:66%"></i><i style="height:61%"></i><i style="height:80%"></i><i style="height:93%"></i></div></div><div class="chart-dates"><span>Wed</span><span>Thu</span><span>Fri</span><span>Sat</span><span>Sun</span><span>Mon</span><span>Today</span></div></div></div></article>
      <article class="panel"><div class="panel-header"><div><h2>Activity</h2><p>Important changes today</p></div><button class="panel-link" data-page="security">View all →</button></div><ul class="activity"><li><b class="activity-icon green">₦</b><div><strong>Payment verified</strong><span>₦85,000 received from BrightWorks Ltd.</span><time>12 minutes ago</time></div></li><li><b class="activity-icon">+</b><div><strong>New customer activated</strong><span>Chika Nwosu started on Home Pro 50.</span><time>29 minutes ago</time></div></li><li><b class="activity-icon amber">!</b><div><strong>Router needs review</strong><span>Yaba POP latency crossed its attention threshold.</span><time>47 minutes ago</time></div></li><li><b class="activity-icon green">✓</b><div><strong>Voucher batch used</strong><span>Campus hotspot batch is now 86% redeemed.</span><time>1 hour ago</time></div></li></ul></article>
    </section>
    <section class="bottom-grid"><article class="panel"><div class="panel-header"><div><h2>Network health</h2><p>Across 18 managed locations</p></div><button class="panel-link" data-page="network">Details →</button></div><div class="health-list"><div class="health-row"><i class="health-dot"></i><strong>RADIUS authentication</strong><span>Healthy</span></div><div class="health-row"><i class="health-dot"></i><strong>Core routers</strong><span>18 / 18 online</span></div><div class="health-row warn"><i class="health-dot"></i><strong>Yaba POP</strong><span>Elevated latency</span></div></div></article><article class="panel"><div class="panel-header"><div><h2>Today’s work</h2><p>Small queue, clear priorities</p></div><button class="panel-link" data-page="automations">Open queue →</button></div><div class="task-list"><div class="task done"><b class="task-check">✓</b><span>Confirm payment reconciliation</span><time>Done</time></div><div class="task"><b class="task-check">✓</b><span>Review 4 expiring subscriptions</span><time>Today</time></div><div class="task"><b class="task-check">✓</b><span>Check Yaba POP alert</span><time>Today</time></div></div></article><article class="panel"><div class="panel-header"><div><h2>Ready to expand?</h2><p>One setup item remaining</p></div></div><div class="mini-stat"><p>Payment provider</p><strong>Connect</strong></div><div class="mini-stat"><p>Secret store</p><strong>Adapter</strong></div><button class="button primary" type="button" data-page="settings">Open settings</button></article></section>`;
}

function standardPage(id) {
  const page = pages[id];
  const status = id === "network" ? `<div class="status"><i></i><strong>18 of 18 locations are reachable</strong><span>One location is above its normal latency range.</span><span class="updated">Last checked 38 sec ago</span></div>` : id === "security" ? `<div class="status"><i></i><strong>Security posture is strong</strong><span>All privileged operators have MFA enabled except one pending invite.</span><span class="updated">Checked just now</span></div>` : "";
  return `${heading(page)}${status}${metrics(page.side)}<section class="split-grid">${table(page)}<aside class="panel"><div class="panel-header"><div><h2>${id === "settings" ? "Environment" : "At a glance"}</h2><p>Representative preview data</p></div></div><ul class="detail-list">${page.side.map(([label, value]) => `<li><span>${label}</span><strong>${value}</strong></li>`).join("")}</ul></aside></section>`;
}

function accessHeading(title, description) {
  return `<div class="page-heading"><div><p class="kicker">Secure control dashboard</p><h1>${title}</h1><p class="description">${description}</p></div><div class="heading-actions"></div></div>`;
}

function dashboard() {
  return `${accessHeading("Operations overview", "Only verified, authorised data is displayed in this workspace.")}
    <div class="status"><i></i><strong>Authorised session established</strong><span>Dashboard summaries are awaiting their live data source.</span><span class="updated">Secure session</span></div>
    ${metrics([["Active customers", "—"], ["Online sessions", "—"], ["Collected today", "—"], ["Needs attention", "—"]])}
    <section class="dashboard-grid"><article class="panel"><div class="panel-header"><div><h2>Collection trend</h2><p>Verified payment data will appear here when the reporting endpoint is enabled.</p></div></div><p class="description">No representative values are shown in the production control dashboard.</p></article><article class="panel"><div class="panel-header"><div><h2>Activity</h2><p>Recent authorised activity will appear here.</p></div></div><p class="description">No verified activity is available yet.</p></article></section>
    <section class="bottom-grid"><article class="panel"><div class="panel-header"><div><h2>Network health</h2><p>Live network health endpoint required.</p></div></div><p class="description">Awaiting verified service status.</p></article><article class="panel"><div class="panel-header"><div><h2>Today’s work</h2><p>Live operational queue required.</p></div></div><p class="description">No verified queue is available yet.</p></article><article class="panel"><div class="panel-header"><div><h2>Production controls</h2><p>Server-side permissions remain authoritative.</p></div></div><p class="description">Actions are enabled only as their audited API workflows are delivered.</p></article></section>`;
}

function standardPage(id) {
  const page = pages[id];
  const neutralPage = { ...page, rows: [], side: page.side.map(([label]) => [label, "—"]) };
  const emptyTable = `<tr><td colspan="${page.cols.length}" class="empty-cell">Loading authorised live data. If this remains empty, your role may not have permission or this endpoint is not yet enabled.</td></tr>`;
  const liveTable = `<section class="panel table"><div class="toolbar"><div class="live-list-controls" data-live-list-controls="${id}"></div><nav class="live-list-pagination" data-live-list-pagination="${id}" aria-label="${page.title} pages"></nav></div><table class="data-table"><thead><tr>${page.cols.map((column) => `<th>${column}</th>`).join("")}</tr></thead><tbody>${emptyTable}</tbody></table></section>`;
  return `${heading(page, id)}<div class="status"><i></i><strong>Authorised data only</strong><span>This view stays empty until its server-side data source responds.</span><span class="updated">Secure session</span></div>${metrics(neutralPage.side)}<section class="split-grid">${liveTable}<aside class="panel"><div class="panel-header"><div><h2>${id === "settings" ? "Environment" : "At a glance"}</h2><p>Verified values only</p></div></div><ul class="detail-list">${neutralPage.side.map(([label, value]) => `<li><span>${label}</span><strong>${value}</strong></li>`).join("")}</ul></aside></section>`;
}

function render(id) {
  if (!adminState.authorised) return;
  const pageId = pages[id] ? id : "overview";
  content.innerHTML = pageId === "overview" ? dashboard() : standardPage(pageId);
  document.title = `${pages[pageId].title} · NetCore`;
  navItems.forEach((item) => item.classList.toggle("active", item.dataset.page === pageId));
  content.focus({ preventScroll: true });
  sidebar.classList.remove("open");
  window.dispatchEvent(new CustomEvent("netcore:page-rendered", { detail: pageId }));
}
function navigate(id) { const target = pages[id] ? id : "overview"; if (location.hash.slice(1) !== target) location.hash = target; else render(target); }
function showToast(message) { if (!adminState.authorised) return; toast.textContent = message; toast.classList.add("visible"); clearTimeout(toastTimer); toastTimer = setTimeout(() => toast.classList.remove("visible"), 2800); }
function fillCommandOptions(filter = "") { const query = filter.trim().toLowerCase(); const choices = Object.entries(pages).filter(([, page]) => page.title.toLowerCase().includes(query) || page.kicker.toLowerCase().includes(query)); commandOptions.innerHTML = choices.map(([id,page]) => `<button class="command-option" type="button" data-page="${id}"><span>${icons[id]}</span><strong>${page.title}</strong><small>${page.kicker}</small></button>`).join("") || `<p class="description">No page matches that search.</p>`; }
function openCommand() { if (!adminState.authorised) return; fillCommandOptions(); modal.hidden = false; commandInput.value = ""; setTimeout(() => commandInput.focus(), 0); }
function closeCommand() { modal.hidden = true; document.querySelector("#command-button").focus(); }
function resetCommandOnLoad() { modal.hidden = true; commandInput.value = ""; commandOptions.replaceChildren(); }

document.addEventListener("click", (event) => {
  const pageTarget = event.target.closest("[data-page]");
  if (pageTarget) { event.preventDefault(); navigate(pageTarget.dataset.page); if (!modal.hidden) closeCommand(); }
  const toastTarget = event.target.closest("[data-toast]");
  if (toastTarget) showToast(toastTarget.dataset.toast);
});
document.querySelector("#command-button").addEventListener("click", openCommand);
document.querySelector("#menu-toggle").addEventListener("click", () => sidebar.classList.toggle("open"));
commandInput.addEventListener("input", () => fillCommandOptions(commandInput.value));
modal.addEventListener("click", (event) => { if (event.target === modal) closeCommand(); });
document.addEventListener("keydown", (event) => { if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") { event.preventDefault(); modal.hidden ? openCommand() : closeCommand(); } if (event.key === "Escape" && !modal.hidden) closeCommand(); });
window.addEventListener("hashchange", () => render(location.hash.slice(1)));
window.addEventListener("pageshow", resetCommandOnLoad);

function showAccess(markup) {
  resetCommandOnLoad();
  appShell.hidden = true;
  accessScreen.hidden = false;
  accessScreen.innerHTML = markup;
  accessScreen.focus({ preventScroll: true });
}

function showLocked(reason) {
  showAccess(`<section class="access-card"><div class="brand-mark" aria-hidden="true"><span></span><span></span><span></span></div><p class="kicker">NetCore control dashboard</p><h1>Dashboard access is locked</h1><p class="description">${reason}</p><p class="access-note">This public deployment does not render operational or customer data until it is connected to the private, same-origin production API.</p></section>`);
}

function showLogin(message = "") {
  showAccess(`<section class="access-card"><div class="brand-mark" aria-hidden="true"><span></span><span></span><span></span></div><p class="kicker">NetCore control dashboard</p><h1>Sign in to continue</h1><p class="description">Use your authorised operator account and authenticator code. All access is checked again by the API for every request.</p><form id="admin-login-form" class="login-form"><label>Email address<input name="identifier" type="email" autocomplete="username" required /></label><label>Password<input name="password" type="password" autocomplete="current-password" required /></label><label>Authenticator code<input name="mfa_code" type="text" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" required /></label><p class="login-error" role="alert">${message}</p><button class="button primary" type="submit">Sign in securely</button></form></section>`);
  const form = document.querySelector("#admin-login-form");
  form.addEventListener("submit", submitLogin);
}

function lockExpiredSession() {
  window.NETCORE_PRINCIPAL = null;
  adminState = { authorised: false, adaptersLoaded: true, identity: null };
  showLogin("Your secure session has expired. Sign in to continue.");
}

function scheduleSessionExpiry(expiresAt) {
  cancelSessionExpiry();
  if (!window.NetCoreSessionExpiry || typeof window.NetCoreSessionExpiry.arm !== "function") {
    showLocked("The dashboard could not verify this session expiry safely.");
    return;
  }
  cancelSessionExpiry = window.NetCoreSessionExpiry.arm(expiresAt, { onExpired: lockExpiredSession });
}

async function submitLogin(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const submit = form.querySelector("button[type=submit]");
  const identifier = form.elements.identifier.value.trim();
  let password = form.elements.password.value;
	const mfaCode = form.elements.mfa_code.value.trim();
  submit.disabled = true;
  try {
    const response = await fetch("/auth/login", {
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: { "Content-Type": "application/json", "Accept": "application/json" },
      body: JSON.stringify({ tenant: adminConfig.tenant, identifier, password, mfa_code: mfaCode })
    });
    password = "";
    if (!response.ok) {
      showLogin(response.status === 401 ? "The sign-in details were not accepted." : "Sign-in is temporarily unavailable. Please try again later.");
      return;
    }
    await establishSession();
  } catch (_) {
    showLogin("Sign-in is temporarily unavailable. Please try again later.");
  } finally {
    submit.disabled = false;
  }
}

function updateIdentity(identity) {
  const name = identity && identity.email ? identity.email : "Authorised operator";
  document.querySelectorAll("[data-identity-name]").forEach((element) => { element.textContent = name; });
  document.querySelectorAll("[data-workspace-name]").forEach((element) => { element.textContent = adminConfig.tenant; });
}

function loadAdapter(path) {
  return new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = path;
    script.async = false;
    script.onload = resolve;
    script.onerror = () => reject(new Error(`Could not load ${path}`));
    document.head.appendChild(script);
  });
}

async function loadLiveAdapters() {
  if (adminState.adaptersLoaded) return;
  // The production dashboard only talks to its own origin. Caddy proxies
  // /api and /auth to the private API service; no cross-origin admin session
  // or browser-provided API target is allowed.
  window.NETCORE_API_URL = window.location.origin;
  for (const path of liveAdapterPaths) await loadAdapter(path);
  adminState.adaptersLoaded = true;
}

async function establishSession() {
  let response;
  try {
    response = await fetch("/api/v1/me", { credentials: "same-origin", cache: "no-store", headers: { "Accept": "application/json" } });
  } catch (_) {
    showLocked("The secure API is not reachable from this dashboard origin.");
    return;
  }
  if (response.status === 401) {
    showLogin();
    return;
  }
  if (!response.ok) {
    showLocked("The secure API did not accept this dashboard connection.");
    return;
  }
  let payload;
  try {
    payload = await response.json();
  } catch (_) {
    showLocked("The secure API returned an invalid session response.");
    return;
  }
  if (!payload || !payload.user || !Array.isArray(payload.user.permissions)) {
    showLocked("The secure API did not return an authorised operator session.");
    return;
  }
  try {
	window.NETCORE_PRINCIPAL = payload.user;
    await loadLiveAdapters();
  } catch (_) {
	window.NETCORE_PRINCIPAL = null;
    showLocked("The dashboard assets could not be loaded safely. Try again after the deployment is complete.");
    return;
  }
  adminState = { authorised: true, adaptersLoaded: true, identity: payload.user };
  updateIdentity(payload.user);
  accessScreen.hidden = true;
  appShell.hidden = false;
  resetCommandOnLoad();
  scheduleSessionExpiry(payload.expires_at);
  render(location.hash.slice(1) || "overview");
}

async function logout() {
  cancelSessionExpiry();
  cancelSessionExpiry = () => {};
  try {
    const response = await fetch("/auth/logout", { method: "POST", credentials: "same-origin", cache: "no-store" });
    if (!response.ok) throw new Error("logout rejected");
    // Reloading discards every in-memory adapter cache before another operator
    // can sign in on this shared device.
    window.location.replace("/");
  } catch (_) {
	window.NETCORE_PRINCIPAL = null;
    adminState = { authorised: false, adaptersLoaded: false, identity: null };
    showLocked("The session could not be closed safely. Check your connection and close this browser window.");
  }
}

logoutButton.addEventListener("click", logout);
resetCommandOnLoad();
if (!hasLiveConfig) {
  showLocked("This environment has not been configured for secure production access.");
} else {
  establishSession();
}
