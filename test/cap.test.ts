import assert from "node:assert/strict";
import { test } from "node:test";
import { CapNegotiation } from "../src/cap.ts";

test("CAP REQ is all-or-nothing: partial unknown list is fully NAKed", () => {
  const cap = new CapNegotiation();
  const result = cap.handleReq(["message-tags", "echo-message"]);
  assert.equal(result.acknowledged, false);
  assert.deepEqual(result.capabilities, ["message-tags", "echo-message"]);
  // No partial enablement on NAK.
  assert.equal(cap.has("message-tags"), false);
});

test("CAP REQ ACKs and enables an all-supported list", () => {
  const cap = new CapNegotiation();
  const result = cap.handleReq(["message-tags"]);
  assert.equal(result.acknowledged, true);
  assert.equal(cap.messageTags, true);
});

test("an empty REQ is never acknowledged", () => {
  const cap = new CapNegotiation();
  assert.equal(cap.handleReq([]).acknowledged, false);
});

test("CAP LS 302 holds registration until CAP END", () => {
  const cap = new CapNegotiation();
  cap.handleLs(302);
  assert.equal(cap.registrationHeld, true);
  cap.handleEnd();
  assert.equal(cap.registrationHeld, false);
});

test("CAP LS below 302 does not hold registration", () => {
  const cap = new CapNegotiation();
  cap.handleLs(301);
  assert.equal(cap.registrationHeld, false);
  cap.handleLs(300);
  assert.equal(cap.registrationHeld, false);
});
