import { test } from "node:test";
import assert from "node:assert/strict";
import { makeHarness, invite, consent, answer } from "./helpers.ts";
import { classifyRecovery, runReplayReference } from "../src/index.ts";

test("the packaged two-node replay reference passes all five scenarios", async () => {
  const report = await runReplayReference();
  for (const scenario of report.scenarios) {
    for (const check of scenario.checks) {
      assert.equal(check.pass, true, `${scenario.scenario}: ${check.name} (${check.detail})`);
    }
  }
  assert.equal(report.allPassed, true);
});

test("recovery distinguishes delivered, reconciled and indeterminate outcomes", async () => {
  const h = makeHarness();
  const sessionId = "s-recovery";
  const invitation = invite(h, sessionId, "g1");
  await h.nodeA.submitFact(
    sessionId,
    h.sig.signFact(h.alice, "invitation", invitation, { signedAt: h.clock.now }),
  );
  await h.nodeA.submitFact(sessionId, consent(h, invitation, h.alice));
  await h.nodeA.submitFact(sessionId, consent(h, invitation, h.bob));
  await h.nodeA.registerConnection(sessionId, h.bob.identity, "bob", "addr", 1);

  for (const answerId of ["d1", "d2", "d3"]) {
    await h.nodeA.submitFact(sessionId, answer(h, invitation, answerId, h.alice, h.bob));
  }

  await h.nodeA.attemptDelivery(sessionId, "d1");
  const knownReceipt = await h.receipts.getByAnswer("d1");
  assert.ok(knownReceipt);
  await h.nodeA.confirmDelivery(sessionId, "d1", knownReceipt!.receiptId);

  await h.nodeA.attemptDelivery(sessionId, "d2");

  const delivered = await h.nodeA.recoverAnswer(sessionId, "d1");
  assert.equal(delivered.kind, "delivered");

  const reconciled = await h.nodeA.recoverAnswer(sessionId, "d2");
  assert.equal(reconciled.kind, "delivered-reconciled");

  const notAttempted = await h.nodeA.recoverAnswer(sessionId, "d3");
  assert.equal(notAttempted.kind, "not-attempted");

  await h.nodeA.attemptDelivery(sessionId, "d3");
  const state = await h.nodeA.getState(sessionId);
  const indeterminate = await classifyRecovery(state, "d3", async () => null);
  assert.equal(indeterminate.kind, "indeterminate");
});
