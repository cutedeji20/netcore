"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const vouchers = require("./portal-vouchers.js");

test("voucher codes are normalized and masked without revealing the full code", () => {
  assert.equal(vouchers.normalizeVoucherCode(" abcd-1234-5678 "), "ABCD12345678");
  assert.equal(vouchers.maskVoucherCode("ABCD12345678"), "ABCD••••5678");
  assert.equal(vouchers.maskVoucherCode("A"), "A••••");
});

test("new short and previously issued long voucher codes are accepted", () => {
  assert.equal(vouchers.validateVoucherCode("NC-ABCDEF-GHJKLM-NPQRST"), "");
  assert.equal(vouchers.validateVoucherCode("NC-ABCDEFGH-JKLMNPQR-STUVWXYZ-23456723"), "");
  assert.equal(vouchers.validateVoucherCode(""), "Enter a voucher code.");
  assert.equal(vouchers.validateVoucherCode("short"), "Enter a valid voucher code.");
  assert.equal(vouchers.validateVoucherCode("bad!code"), "Voucher codes use letters and numbers only.");
});

test("guest requests only include the normalized code and connection context", () => {
  const connection = {
    client_mac: "00:11:22:33:44:55",
    nas_address: "10.10.0.1",
    hotspot_login_url: "https://portal.example/login"
  };

  assert.deepEqual(vouchers.guestRequest("  NC-ABCDEF-GHJKLM-NPQRST  ", connection), {
    code: "NCABCDEFGHJKLMNPQRST",
    client_mac: "00:11:22:33:44:55",
    nas_address: "10.10.0.1",
    hotspot_login_url: "https://portal.example/login"
  });
  assert.equal(vouchers.guestRequest("abc", connection), null);
  assert.equal(vouchers.guestRequest("NC-ABCDEF-GHJKLM-NPQRST", { client_mac: "", nas_address: "10.10.0.1", hotspot_login_url: "https://portal.example/login" }), null);
});
