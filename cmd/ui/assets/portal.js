(function () {
  "use strict";

  // The production build replaces portal-config.js from deployment data. A
  // tenant from the URL would let a guest choose another tenant's login scope,
  // so it is intentionally never read from browser context.
  var portalConfig = window.NETCORE_PORTAL_CONFIG || {};
  var liveMode = portalConfig.mode === "live" && typeof portalConfig.tenant === "string" && portalConfig.tenant.length > 0;
  var connection = readConnectionContext();

  var views = {
    home: document.querySelector("#access-view"),
    plans: document.querySelector("#plans-view"),
    login: document.querySelector("#login-view")
  };
  var planStatus = document.querySelector("#plan-status");
  var loginStatus = document.querySelector("#login-status");
  var loginForm = document.querySelector("#portal-login");
  var loginSubmit = loginForm.querySelector("button[type=submit]");

  function showView(name) {
    Object.keys(views).forEach(function (key) {
      views[key].hidden = key !== name;
      views[key].classList.toggle("active", key === name);
    });
    if (name === "plans") planStatus.textContent = "";
    if (name === "login") loginStatus.textContent = "";
  }

  document.addEventListener("click", function (event) {
    var action = event.target.closest("[data-action]");
    if (action) {
      if (action.dataset.action === "home") showView("home");
      if (action.dataset.action === "plans") showView("plans");
      if (action.dataset.action === "login") showView("login");
    }

    var plan = event.target.closest("[data-plan]");
    if (plan) {
      planStatus.textContent = liveMode
        ? plan.dataset.plan + " selected. Payment will be available here once your provider is connected."
        : plan.dataset.plan + " selected. Secure payment is the next step in the live portal.";
    }
  });

  loginForm.addEventListener("submit", function (event) {
    event.preventDefault();
    if (!liveMode) {
      loginStatus.textContent = "Preview only — a live sign-in will securely check your account and active plan.";
      return;
    }
    if (!connection) {
      loginStatus.textContent = "Open this page from NetCore Wi-Fi to continue this connection.";
      return;
    }
    signInAndHandoff();
  });

  function readConnectionContext() {
    var query = new URLSearchParams(window.location.search);
    var context = {
      client_mac: query.get("client_mac") || "",
      nas_address: query.get("nas_address") || "",
      hotspot_login_url: query.get("hotspot_login_url") || ""
    };
    if (!context.client_mac || !context.nas_address || !context.hotspot_login_url) {
      return null;
    }
    return context;
  }

  function endpoint(path) {
    return String(portalConfig.apiBase || "").replace(/\/$/, "") + path;
  }

  function responseBody(response) {
    return response.json().catch(function () {
      return {};
    });
  }

  function humanError(body, fallback) {
    return body && body.error && body.error.message ? body.error.message : fallback;
  }

  function setSubmitting(submitting) {
    loginSubmit.disabled = submitting;
    loginSubmit.setAttribute("aria-busy", submitting ? "true" : "false");
  }

  function postJSON(path, payload) {
    return window.fetch(endpoint(path), {
      method: "POST",
      credentials: "include",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload)
    }).then(function (response) {
      return responseBody(response).then(function (body) {
        return { response: response, body: body };
      });
    });
  }

  function signInAndHandoff() {
    var fields = new FormData(loginForm);
    loginStatus.textContent = "Checking your account…";
    setSubmitting(true);

    postJSON("/auth/login", {
      tenant: portalConfig.tenant,
      identifier: fields.get("identifier"),
      password: fields.get("password")
    }).then(function (login) {
      if (!login.response.ok) {
        throw new Error(humanError(login.body, "We could not sign you in. Please try again."));
      }
      return postJSON("/api/v1/portal/handoff", connection);
    }).then(function (handoff) {
      if (handoff.response.status === 409 && handoff.body && handoff.body.error && handoff.body.error.code === "NO_ACTIVE_PLAN") {
        showView("plans");
        planStatus.textContent = handoff.body.error.message;
        return;
      }
      if (!handoff.response.ok || !handoff.body.redirect_url) {
        throw new Error(humanError(handoff.body, "We could not finish this connection. Please try again."));
      }

      // The API validates this RouterOS URL against the registered NAS and
      // issues a 120-second, single-use token. Do not log or persist it.
      window.location.replace(handoff.body.redirect_url);
    }).catch(function (error) {
      loginStatus.textContent = error && error.message ? error.message : "We could not continue right now. Please try again shortly.";
    }).finally(function () {
      setSubmitting(false);
    });
  }
}());
