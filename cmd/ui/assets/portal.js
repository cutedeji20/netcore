(function () {
  "use strict";

  // The API resolves the portal tenant from deployment configuration. A tenant
  // from the URL would let a guest choose another login or catalogue scope,
  // so it is intentionally never read from browser context.
  var portalConfig = window.NETCORE_PORTAL_CONFIG || {};
  var liveMode = portalConfig.mode === "live";
  var accountsEnabled = liveMode && portalConfig.accountsEnabled === true;
  var paymentsEnabled = liveMode && portalConfig.paymentsEnabled === true;
  var checkoutStorage = window.NetCorePortalCheckout || null;
  var accountPresentation = window.NetCorePortalAccount || null;
  var recovery = window.NetCorePortalRecovery || null;
  var postLoginNavigation = window.NetCorePortalNavigation || null;
  var registration = window.NetCorePortalRegistration || null;
  var connection = readConnectionContext();
  var storedPaymentReturn = checkoutStorage ? checkoutStorage.readPaymentReturn(window.sessionStorage) : null;
  if (!connection && storedPaymentReturn) connection = storedPaymentReturn.connection;
  var returningPaymentReference = readReturnedPaymentReference() || (storedPaymentReturn && storedPaymentReturn.reference) || "";

  var views = {
    home: document.querySelector("#access-view"),
    plans: document.querySelector("#plans-view"),
    register: document.querySelector("#register-view"),
    verify: document.querySelector("#verify-view"),
    login: document.querySelector("#login-view"),
    resetRequest: document.querySelector("#reset-request-view"),
    resetConfirm: document.querySelector("#reset-confirm-view"),
    account: document.querySelector("#account-view")
  };
  var planStatus = document.querySelector("#plan-status");
  var planGrid = document.querySelector("#portal-plans");
  var registerStatus = document.querySelector("#register-status");
  var verifyStatus = document.querySelector("#verify-status");
  var loginStatus = document.querySelector("#login-status");
  var resetRequestStatus = document.querySelector("#reset-request-status");
  var resetConfirmStatus = document.querySelector("#reset-confirm-status");
  var registerForm = document.querySelector("#portal-register");
  var verifyForm = document.querySelector("#portal-verify-email");
  var loginForm = document.querySelector("#portal-login");
  var resetRequestForm = document.querySelector("#portal-reset-request");
  var resetConfirmForm = document.querySelector("#portal-reset-confirm");
  var loginSubmit = loginForm.querySelector("button[type=submit]");
  var accountStatus = document.querySelector("#account-status");
  var accountSubscriptions = document.querySelector("#account-subscriptions");
  var accountPayments = document.querySelector("#account-payments");
  var catalogueLoading = false;
  var catalogueLoaded = false;
  var selectedPlanName = "";
  var selectedPlanID = "";
  var pendingRegistration = null;
  var pendingPasswordReset = null;
  var customerAuthenticated = false;
  var checkoutPending = false;
  var accountRequested = false;

  function showView(name) {
    Object.keys(views).forEach(function (key) {
      views[key].hidden = key !== name;
      views[key].classList.toggle("active", key === name);
    });
    if (name === "plans") loadCatalogue();
    if (name === "register") registerStatus.textContent = selectedPlanName ? "Continue with " + selectedPlanName + "." : "";
    if (name === "login") loginStatus.textContent = "";
  }

  document.addEventListener("click", function (event) {
    var action = event.target.closest("[data-action]");
    if (action) {
      if (action.dataset.action === "home") showView("home");
      if (action.dataset.action === "plans") showView("plans");
      if (action.dataset.action === "register") showView("register");
      if (action.dataset.action === "login") showView("login");
      if (action.dataset.action === "reset-request") showView("resetRequest");
      if (action.dataset.action === "account") {
        accountRequested = true;
        if (customerAuthenticated) {
          showView("account");
          loadCustomerAccount();
        } else {
          showView("login");
        }
      }
      if (action.dataset.action === "renew") {
        accountRequested = false;
        showView("plans");
      }
    }

    var plan = event.target.closest("[data-plan-id]");
    if (plan) {
      if (!liveMode) {
        planStatus.textContent = "Preview only — published plans appear here when the portal is live.";
        return;
      }
      if (!accountsEnabled) {
        planStatus.textContent = "Account setup is not available yet. Please contact support to purchase access.";
        return;
      }
      selectedPlanID = String(plan.dataset.planId || "");
      selectedPlanName = plan.dataset.planName || "your selected plan";
      if (customerAuthenticated) {
        beginCheckout();
        return;
      }
      showView("register");
    }
  });

  registerForm.addEventListener("submit", function (event) {
    event.preventDefault();
    if (!liveMode) {
      registerStatus.textContent = "Preview only — account creation is available in the live portal.";
      return;
    }
    if (!accountsEnabled) {
      registerStatus.textContent = "Account setup is not available yet. Please contact support for access.";
      return;
    }
    var fields = new FormData(registerForm);
    var email = String(fields.get("email") || "");
    var password = String(fields.get("password") || "");
    var registrationMessage = registration ? registration.validate(email, password, String(fields.get("confirm_password") || "")) : "Account setup is temporarily unavailable. Please try again shortly.";
    if (registrationMessage) {
      registerStatus.textContent = registrationMessage;
      return;
    }
    setFormSubmitting(registerForm, true);
    registerStatus.textContent = "Sending your verification code…";
    postJSON("/portal/auth/register", { email: email, password: password }).then(function (result) {
      if (!result.response.ok || !result.body.challenge_id) {
        throw new Error(humanError(result.body, "We could not send a verification code. Please try again."));
      }
      pendingRegistration = {
        email: email,
        password: password,
        challengeID: String(result.body.challenge_id)
      };
      showView("verify");
      verifyStatus.textContent = "We sent a verification code to " + pendingRegistration.email + ".";
    }).catch(function (error) {
      registerStatus.textContent = error && error.message ? error.message : "We could not continue right now. Please try again shortly.";
    }).finally(function () {
      setFormSubmitting(registerForm, false);
    });
  });

  verifyForm.addEventListener("submit", function (event) {
    event.preventDefault();
    if (!accountsEnabled) {
      verifyStatus.textContent = "Email verification is not available yet. Please contact support for access.";
      return;
    }
    if (!pendingRegistration) {
      showView("register");
      registerStatus.textContent = "Start account creation again to receive a new verification code.";
      return;
    }
    var fields = new FormData(verifyForm);
    setFormSubmitting(verifyForm, true);
    verifyStatus.textContent = "Verifying your email…";
    postJSON("/portal/auth/verify-email", {
      email: pendingRegistration.email,
      challenge_id: pendingRegistration.challengeID,
      code: fields.get("code")
    }).then(function (result) {
      if (!result.response.ok) {
        throw new Error(humanError(result.body, "The verification code is invalid or has expired."));
      }
      loginForm.elements.identifier.value = pendingRegistration.email;
      loginForm.elements.password.value = pendingRegistration.password;
      pendingRegistration = null;
      showView("login");
      loginStatus.textContent = "Email verified. Sign in to continue.";
    }).catch(function (error) {
      verifyStatus.textContent = error && error.message ? error.message : "We could not verify that code. Please try again.";
    }).finally(function () {
      setFormSubmitting(verifyForm, false);
    });
  });

  resetRequestForm.addEventListener("submit", function (event) {
    event.preventDefault();
    if (!liveMode || !accountsEnabled) {
      resetRequestStatus.textContent = "Password recovery is not available yet. Please contact support for access.";
      return;
    }
    if (!recovery) {
      resetRequestStatus.textContent = "Password recovery is temporarily unavailable. Please try again shortly.";
      return;
    }
    var fields = new FormData(resetRequestForm);
    var email = String(fields.get("email") || "");
    setFormSubmitting(resetRequestForm, true);
    resetRequestStatus.textContent = "Sending your recovery code…";
    postJSON("/portal/auth/password-reset/request", { email: email }).then(function (result) {
      if (!result.response.ok || !result.body.challenge_id) {
        throw new Error(humanError(result.body, "We could not send a recovery code. Please try again."));
      }
      pendingPasswordReset = recovery.createChallenge(email, String(result.body.challenge_id));
      if (!pendingPasswordReset) {
        throw new Error("We could not start password recovery. Please try again.");
      }
      showView("resetConfirm");
      resetConfirmStatus.textContent = "If this address can receive NetCore recovery messages, a code is on its way.";
    }).catch(function (error) {
      resetRequestStatus.textContent = error && error.message ? error.message : "We could not continue right now. Please try again shortly.";
    }).finally(function () {
      setFormSubmitting(resetRequestForm, false);
    });
  });

  resetConfirmForm.addEventListener("submit", function (event) {
    event.preventDefault();
    if (!recovery || !pendingPasswordReset) {
      showView("resetRequest");
      resetRequestStatus.textContent = "Start password recovery again to receive a new code.";
      return;
    }
    var fields = new FormData(resetConfirmForm);
    var password = String(fields.get("password") || "");
    if (!recovery.canConfirm(password, String(fields.get("confirm_password") || ""))) {
      resetConfirmStatus.textContent = "Passwords must match and be at least 12 characters.";
      return;
    }
    setFormSubmitting(resetConfirmForm, true);
    resetConfirmStatus.textContent = "Resetting your password…";
    postJSON("/portal/auth/password-reset/confirm", {
      email: pendingPasswordReset.email,
      challenge_id: pendingPasswordReset.challengeID,
      code: fields.get("code"),
      password: password
    }).then(function (result) {
      if (!result.response.ok) {
        throw new Error(humanError(result.body, "The recovery code is invalid or has expired."));
      }
      var email = pendingPasswordReset.email;
      pendingPasswordReset = null;
      resetConfirmForm.reset();
      loginForm.elements.identifier.value = email;
      showView("login");
      loginStatus.textContent = "Password reset. Sign in to continue.";
    }).catch(function (error) {
      resetConfirmStatus.textContent = error && error.message ? error.message : "We could not reset this password. Please try again.";
    }).finally(function () {
      setFormSubmitting(resetConfirmForm, false);
    });
  });

  loginForm.addEventListener("submit", function (event) {
    event.preventDefault();
    if (!liveMode) {
      loginStatus.textContent = "Preview only — a live sign-in will securely check your account and active plan.";
      return;
    }
    if (!accountsEnabled) {
      loginStatus.textContent = "Customer sign-in is not available yet. Please contact support for access.";
      return;
    }
    if (!connection && !returningPaymentReference && !accountRequested) {
      loginStatus.textContent = "Open this page from NetCore Wi-Fi to continue this connection.";
      return;
    }
    signInAndContinue();
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

  function readReturnedPaymentReference() {
    if (!checkoutStorage) return "";
    var query = new URLSearchParams(window.location.search);
    var reference = query.get("reference") || "";
    if (!checkoutStorage.validReference(reference)) return "";
    // The reference is not a credential, but it still has no value in the
    // address bar after we have captured it for a server-side verification.
    if (window.history && window.history.replaceState) {
      window.history.replaceState(null, "", window.location.pathname);
    }
    return reference;
  }

  function loadCatalogue() {
    if (!liveMode) {
      planGrid.replaceChildren();
      planStatus.textContent = "Preview only — published plans will appear here when the portal is live.";
      return;
    }
    if (catalogueLoading || catalogueLoaded) return;
    catalogueLoading = true;
    planStatus.textContent = "Loading available plans…";
    planGrid.replaceChildren();
    window.fetch(endpoint("/api/v1/portal/catalogue"), {
      credentials: "include",
      headers: { "Accept": "application/json" }
    }).then(function (response) {
      return responseBody(response).then(function (body) {
        if (!response.ok) throw new Error(humanError(body, "We could not load available plans."));
        return body;
      });
    }).then(function (body) {
      renderCatalogue(body && body.data);
      catalogueLoaded = true;
    }).catch(function (error) {
      planStatus.textContent = error && error.message ? error.message : "We could not load available plans.";
    }).finally(function () {
      catalogueLoading = false;
    });
  }

  function renderCatalogue(plans) {
    planGrid.replaceChildren();
    if (!Array.isArray(plans) || plans.length === 0) {
      planStatus.textContent = "No plans are available right now. Please contact support.";
      return;
    }
    plans.forEach(function (plan) {
      var option = document.createElement("button");
      option.className = "plan-option";
      option.type = "button";
      option.dataset.planId = String(plan.id || "");
      option.dataset.planName = String(plan.name || "Plan");

      var top = document.createElement("span");
      top.className = "plan-top";
      var name = document.createElement("b");
      name.textContent = String(plan.name || "Internet plan");
      var duration = document.createElement("em");
      duration.textContent = formatDuration(plan.duration_seconds);
      top.append(name, duration);

      var price = document.createElement("strong");
      price.textContent = formatMoney(plan.price_minor, plan.currency);
      var detail = document.createElement("small");
      detail.textContent = plan.description || accessHighlight(plan);
      option.append(top, price, detail);
      planGrid.append(option);
    });
    planStatus.textContent = "";
  }

  function loadCustomerAccount() {
    if (!liveMode || !accountsEnabled) {
      showView("login");
      loginStatus.textContent = "Customer account access is not available yet. Please contact support.";
      return Promise.resolve();
    }
    if (!customerAuthenticated) {
      accountRequested = true;
      showView("login");
      return Promise.resolve();
    }
    accountStatus.textContent = "Loading your account…";
    return window.fetch(endpoint("/api/v1/portal/account"), {
      credentials: "include",
      headers: { "Accept": "application/json" }
    }).then(function (response) {
      return responseBody(response).then(function (body) {
        return { response: response, body: body };
      });
    }).then(function (result) {
      if (result.response.status === 401) {
        customerAuthenticated = false;
        accountRequested = true;
        showView("login");
        loginStatus.textContent = "Sign in to view your plan and payments.";
        return;
      }
      if (!result.response.ok) {
        throw new Error(humanError(result.body, "We could not load your account. Please try again."));
      }
      renderCustomerAccount(accountPresentation ? accountPresentation.displayModel(result.body) : { subscriptions: [], payments: [] });
      accountStatus.textContent = "";
    }).catch(function (error) {
      accountStatus.textContent = error && error.message ? error.message : "We could not load your account. Please try again shortly.";
    });
  }

  function renderCustomerAccount(account) {
    accountSubscriptions.replaceChildren();
    accountPayments.replaceChildren();
    if (!account || !Array.isArray(account.subscriptions) || account.subscriptions.length === 0) {
      appendAccountEmpty(accountSubscriptions, "No plans are linked to this account yet. Choose a plan to start access.");
    } else {
      account.subscriptions.forEach(function (subscription) {
        var row = accountRow(subscription.planName, subscription.status, subscription.status === "ACTIVE" && subscription.expiresAt ? "Expires " + formatPortalDate(subscription.expiresAt) : subscription.startsAt ? "Starts " + formatPortalDate(subscription.startsAt) : "Waiting for payment confirmation");
        var detail = document.createElement("small");
        detail.textContent = "Payment: " + formatState(subscription.paymentStatus);
        row.append(detail);
        accountSubscriptions.append(row);
      });
    }
    if (!account || !Array.isArray(account.payments) || account.payments.length === 0) {
      appendAccountEmpty(accountPayments, "No payments have been recorded for this account yet.");
    } else {
      account.payments.forEach(function (payment) {
        var row = accountRow(formatMoney(payment.amountMinor, payment.currency), payment.status, "Paid on " + formatPortalDate(payment.createdAt));
        var detail = document.createElement("small");
        detail.textContent = "Reference: " + payment.reference;
        row.append(detail);
        accountPayments.append(row);
      });
    }
  }

  function accountRow(title, status, detail) {
    var row = document.createElement("article");
    row.className = "account-row";
    var top = document.createElement("div");
    top.className = "account-row-top";
    var heading = document.createElement("b");
    heading.textContent = title;
    var badge = document.createElement("span");
    badge.className = "account-status " + String(status || "").toLowerCase();
    badge.textContent = formatState(status);
    top.append(heading, badge);
    var description = document.createElement("small");
    description.textContent = detail;
    row.append(top, description);
    return row;
  }

  function appendAccountEmpty(container, message) {
    var empty = document.createElement("p");
    empty.className = "account-empty";
    empty.textContent = message;
    container.append(empty);
  }

  function formatPortalDate(value) {
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) return "an upcoming date";
    try {
      return new Intl.DateTimeFormat("en-NG", { dateStyle: "medium", timeStyle: "short" }).format(date);
    } catch (_) {
      return date.toLocaleString();
    }
  }

  function formatState(value) {
    return String(value || "Unknown").toLowerCase().replace(/_/g, " ").replace(/\b\w/g, function (character) { return character.toUpperCase(); });
  }

  function formatDuration(value) {
    var seconds = Number(value);
    if (!Number.isFinite(seconds) || seconds <= 0) return "Fixed-duration access";
    if (seconds % 86400 === 0) {
      var days = seconds / 86400;
      return days === 1 ? "1 day" : days + " days";
    }
    if (seconds % 3600 === 0) {
      var hours = seconds / 3600;
      return hours === 1 ? "1 hour" : hours + " hours";
    }
    return "Fixed-duration access";
  }

  function formatMoney(minor, currency) {
    var amount = Number(minor) / 100;
    try {
      return new Intl.NumberFormat("en-NG", { style: "currency", currency: String(currency || "NGN") }).format(amount);
    } catch (_) {
      return String(currency || "NGN") + " " + amount.toFixed(2);
    }
  }

  function accessHighlight(plan) {
    var down = Math.round(Number(plan.download_bps) / 1000000);
    var devices = Number(plan.max_devices);
    var parts = [];
    if (Number.isFinite(down) && down > 0) parts.push("Up to " + down + " Mbps");
    if (Number.isFinite(devices) && devices > 0) parts.push(devices === 1 ? "1 device" : devices + " devices");
    return parts.length ? parts.join(" · ") : "Internet access";
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

  function setFormSubmitting(form, submitting) {
    var submit = form.querySelector("button[type=submit]");
    submit.disabled = submitting;
    submit.setAttribute("aria-busy", submitting ? "true" : "false");
  }

  function postJSON(path, payload, headers) {
    var requestHeaders = { "Content-Type": "application/json" };
    if (headers) {
      Object.keys(headers).forEach(function (name) { requestHeaders[name] = headers[name]; });
    }
    return window.fetch(endpoint(path), {
      method: "POST",
      credentials: "include",
      headers: requestHeaders,
      body: JSON.stringify(payload)
    }).then(function (response) {
      return responseBody(response).then(function (body) {
        return { response: response, body: body };
      });
    });
  }

  function signInAndContinue() {
    var fields = new FormData(loginForm);
    loginStatus.textContent = "Checking your account…";
    setSubmitting(true);

    postJSON("/portal/auth/login", {
      identifier: fields.get("identifier"),
      password: fields.get("password")
    }).then(function (login) {
      if (!login.response.ok) {
        throw new Error(humanError(login.body, "We could not sign you in. Please try again."));
      }
      customerAuthenticated = true;
      var destination = postLoginNavigation ? postLoginNavigation.destinationAfterSignIn({
        hasConnection: !!connection,
        hasReturningPayment: !!returningPaymentReference,
        hasSelectedPlan: !!selectedPlanID,
        accountRequested: accountRequested
      }) : (returningPaymentReference ? "payment" : (selectedPlanID ? "checkout" : (accountRequested || !connection ? "account" : "handoff")));
      if (destination === "payment") {
        return confirmReturnedPayment();
      }
      if (destination === "checkout") {
        return beginCheckout();
      }
      if (destination === "account") {
        showView("account");
        return loadCustomerAccount();
      }
      return finishHandoff();
    }).catch(function (error) {
      loginStatus.textContent = error && error.message ? error.message : "We could not continue right now. Please try again shortly.";
    }).finally(function () {
      setSubmitting(false);
    });
  }

  function beginCheckout() {
    if (!paymentsEnabled) {
      planStatus.textContent = "Secure payment is not available yet. Please contact support to purchase access.";
      return Promise.resolve();
    }
    if (!selectedPlanID) {
      planStatus.textContent = "Choose an internet plan before continuing to payment.";
      return Promise.resolve();
    }
    if (!connection) {
      planStatus.textContent = "Open this page from NetCore Wi-Fi to purchase access for this device.";
      return Promise.resolve();
    }
    if (checkoutPending) return Promise.resolve();
    checkoutPending = true;
    planStatus.textContent = "Preparing secure payment for " + selectedPlanName + "…";
    var idempotencyKey = paymentIdempotencyKey(selectedPlanID);
    return postJSON("/api/v1/payments", { plan_id: selectedPlanID }, { "Idempotency-Key": idempotencyKey }).then(function (result) {
      if (!result.response.ok || !result.body.reference || !result.body.authorization_url) {
        throw new Error(humanError(result.body, "We could not start secure payment. Please try again."));
      }
      var checkoutURL = new URL(String(result.body.authorization_url));
      if (checkoutURL.protocol !== "https:") {
        throw new Error("The payment service returned an unsafe checkout link.");
      }
      if (checkoutStorage && checkoutStorage.validReference(String(result.body.reference))) {
        checkoutStorage.rememberPaymentReturn(window.sessionStorage, String(result.body.reference), connection);
      }
      clearPaymentAttempt();
      window.location.assign(checkoutURL.href);
    }).catch(function (error) {
      planStatus.textContent = error && error.message ? error.message : "We could not start secure payment. Please try again shortly.";
    }).finally(function () {
      checkoutPending = false;
    });
  }

  function confirmReturnedPayment() {
    if (!paymentsEnabled) {
      loginStatus.textContent = "Payment confirmation is not available yet. Please contact support with your Paystack receipt.";
      return Promise.resolve();
    }
    if (!returningPaymentReference) return Promise.resolve();
    loginStatus.textContent = "Confirming your payment securely…";
    return postJSON("/api/v1/payments/" + encodeURIComponent(returningPaymentReference) + "/verify", {}).then(function (result) {
      if (result.response.status === 401) {
        showView("login");
        loginStatus.textContent = "Sign in to securely confirm your payment.";
        return;
      }
      if (!result.response.ok) {
        throw new Error(humanError(result.body, "We could not confirm this payment yet. Please try again shortly."));
      }
      returningPaymentReference = "";
      if (checkoutStorage) checkoutStorage.clearPaymentReturn(window.sessionStorage);
      clearPaymentAttempt();
      if (!connection) {
        showView("login");
        loginStatus.textContent = "Payment confirmed. Return to NetCore Wi-Fi to connect this device.";
        return;
      }
      return finishHandoff();
    }).catch(function (error) {
      loginStatus.textContent = error && error.message ? error.message : "We could not confirm this payment yet. Please try again shortly.";
    });
  }

  function finishHandoff() {
    if (!connection) return Promise.resolve();
    return postJSON("/api/v1/portal/handoff", connection).then(function (handoff) {
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
    });
  }

  function paymentIdempotencyKey(planID) {
    var storageKey = "netcore.portal.payment.attempt.v1";
    try {
      var existing = JSON.parse(window.sessionStorage.getItem(storageKey) || "null");
      if (existing && existing.plan_id === planID && typeof existing.key === "string" && existing.key.length >= 16 && existing.key.length <= 200) {
        return existing.key;
      }
      var key = newBrowserKey();
      window.sessionStorage.setItem(storageKey, JSON.stringify({ plan_id: planID, key: key }));
      return key;
    } catch (_) {
      return newBrowserKey();
    }
  }

  function clearPaymentAttempt() {
    try {
      window.sessionStorage.removeItem("netcore.portal.payment.attempt.v1");
    } catch (_) {
      // The server still protects the payment boundary if browser storage fails.
    }
  }

  function newBrowserKey() {
    if (window.crypto && typeof window.crypto.randomUUID === "function") return window.crypto.randomUUID();
    var bytes = new Uint8Array(16);
    if (window.crypto && typeof window.crypto.getRandomValues === "function") {
      window.crypto.getRandomValues(bytes);
      return "portal-" + Array.prototype.map.call(bytes, function (value) { return value.toString(16).padStart(2, "0"); }).join("");
    }
    return "portal-" + String(Date.now()) + "-" + String(Math.random()).slice(2);
  }

  if (returningPaymentReference && paymentsEnabled) {
    showView("login");
    confirmReturnedPayment();
  }
}());
