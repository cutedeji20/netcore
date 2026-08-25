const assert = require("node:assert/strict");
const test = require("node:test");
const { renderTeamActions, teamErrorMessage, reconcileInvitation, teamMutationRequest, teamStepUpFields } = require("./live-team.js");

test("team actions are absent without team.write", () => {
  assert.equal(renderTeamActions({ permissions: ["team.read"] }).includes("Invite teammate"), false);
});

test("team dialogs display the safe standard error-envelope message", () => {
  assert.equal(teamErrorMessage({ error: { code: "STEP_UP_FAILED", message: "Password or authenticator code was not accepted." } }), "Password or authenticator code was not accepted.");
  assert.equal(teamErrorMessage({ message: "wrong level" }), "The team change could not be completed. Please try again.");
});

test("resend replaces the old invitation projection and revoke removes it", () => {
  const previous = [{ id: "old", email: "ops@example.test", status: "PENDING" }];
  const resent = reconcileInvitation(previous, { id: "new", email: "ops@example.test", status: "PENDING" }, "old");
  assert.deepEqual(resent, [{ id: "new", email: "ops@example.test", status: "PENDING" }]);
  assert.deepEqual(reconcileInvitation(resent, null, "new"), []);
});

test("team mutation request carries the required step-up fields once", () => {
  const request = teamMutationRequest("/api/v1/team/invitations/id/resend", "POST", { password: "current password", mfa_code: "123456" });
  assert.equal(request.method, "POST");
  assert.deepEqual(JSON.parse(request.body), { password: "current password", mfa_code: "123456" });
  assert.equal(request.credentials, "same-origin");
});

test("every sensitive team dialog has current-password and six-digit MFA fields", () => {
  assert.deepEqual(teamStepUpFields(), [
    { label: "Current password", name: "password", type: "password", maxLength: 1024 },
    { label: "Authenticator code", name: "mfa_code", type: "text", pattern: "[0-9]{6}", maxLength: 6 }
  ]);
});
