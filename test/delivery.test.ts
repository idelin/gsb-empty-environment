import { test } from "node:test";
import assert from "node:assert/strict";
import { makeHarness, invite, consent, answer } from "./helpers.ts";

async function establishedSession(h: ReturnType<typeof makeHarness>, sessionId: string, group: string) {
  const invitation = invite(h, sessionId, group);
  await h.nodeA.submitFact(
    sessionId,
    h.sig.signFact(h.alice, "invitation", invitation, { signedAt: h.clock.now }),
  );
  await h.nodeA.submitFact(sessionId, consent(h, invitation, h.alice));
  await h.nodeA.submitFact(sessionId, consent(h, invitation, h.bob));
  return invitation;
}

test("partial log reports exactly which facts are missing", async () => {
  const h = makeHarness();
  const invitation = invite(h, "s-partial", "g1");
  await h.nodeA.submitFact(
    "s-partial",
    h.sig.signFact(h.alice, "invitation", invitation, { signedAt: h.clock.now }),
  );
  await h.nodeA.submitFact("s-partial", consent(h, invitation, h.alice));
  await h.nodeA.submitFact("s-partial", answer(h, invitation, "a1", h.alice, h.bob));

  const verdict = await h.nodeA.queryVerdict("s-partial", "a1");
  assert.equal(verdict.status, "waiting");
  if (verdict.status === "waiting") {
    assert.deepEqual(verdict.missing, [{ kind: "consent", party: h.bob.identity }]);
  }
});

test("no live connection blocks delivery with a clear reason", async () => {
  const h = makeHarness();
  const invitation = await establishedSession(h, "s-noconn", "g1");
  await h.nodeA.registerConnection("s-noconn", h.bob.identity, "bob", "addr", 1);
  await h.nodeA.releaseConnection("s-noconn", h.bob.identity, 1);
  await h.nodeA.submitFact("s-noconn", answer(h, invitation, "a1", h.alice, h.bob));
  const verdict = await h.nodeA.queryVerdict("s-noconn", "a1");
  assert.equal(verdict.status, "blocked");
  if (verdict.status === "blocked") {
    assert.equal(verdict.reason, "no-live-connection");
  }
});

test("a never-registered recipient is reported as a missing connection fact", async () => {
  const h = makeHarness();
  const invitation = await establishedSession(h, "s-missing-conn", "g1");
  await h.nodeA.submitFact(
    "s-missing-conn",
    answer(h, invitation, "a1", h.alice, h.bob),
  );
  const verdict = await h.nodeA.queryVerdict("s-missing-conn", "a1");
  assert.equal(verdict.status, "waiting");
  if (verdict.status === "waiting") {
    assert.deepEqual(verdict.missing, [{ kind: "connection", party: h.bob.identity }]);
  }
});

test("verdict change callback fires once per change and never on repeat", async () => {
  const h = makeHarness();
  const invitation = await establishedSession(h, "s-callback", "g1");
  await h.nodeA.registerConnection("s-callback", h.bob.identity, "bob", "addr", 1);
  await h.nodeA.submitFact("s-callback", answer(h, invitation, "a1", h.alice, h.bob));

  await h.nodeA.queryVerdict("s-callback", "a1");
  await h.nodeA.queryVerdict("s-callback", "a1");
  await h.nodeA.queryVerdict("s-callback", "a1");

  const answerCallbacks = h.notifierA.calls.filter((call) => call.answerId === "a1");
  assert.equal(answerCallbacks.length, 1);
  assert.equal(answerCallbacks[0]?.status, "deliverable");

  await h.nodeA.attemptDelivery("s-callback", "a1");
  const afterAttempt = h.notifierA.calls.filter((call) => call.answerId === "a1");
  assert.equal(afterAttempt.length, 2);
  assert.equal(afterAttempt[1]?.status, "in-flight");
});

test("idempotent receipt claim rejects a conflicting recipient", async () => {
  const h = makeHarness();
  const { CasReceiptDirectory, InMemoryCasStore } = await import("../src/index.ts");
  const directory = new CasReceiptDirectory(new InMemoryCasStore());
  const base = {
    answerId: "r1",
    sessionId: "s-receipt",
    invitationDigest: "digest-1",
    recipient: h.bob.identity,
    generation: 2,
    node: "node-B",
  };
  const first = await directory.claim(base);
  const second = await directory.claim(base);
  assert.equal(second.receiptId, first.receiptId);
  assert.equal(second.state, "received");

  await assert.rejects(
    directory.claim({ ...base, recipient: h.carol.identity }),
    /receipt conflict/,
  );
  await directory.markConsumed("r1");
  const consumed = await directory.getByAnswer("r1");
  assert.equal(consumed?.state, "consumed");
});

test("duplicate fact ids are not appended twice", async () => {
  const h = makeHarness();
  const invitation = invite(h, "s-dup", "g1");
  const signed = h.sig.signFact(h.alice, "invitation", invitation, {
    signedAt: h.clock.now,
    factId: "fixed-fact-id",
  });
  assert.equal((await h.nodeA.submitFact("s-dup", signed)).ok, true);
  const duplicate = await h.nodeA.submitFact("s-dup", signed);
  assert.equal(duplicate.ok, false);
  if (!duplicate.ok) {
    assert.equal(duplicate.reason, "malformed-fact");
  }
  const events = await h.store.get<{ events: unknown[] }>("session:s-dup");
  assert.equal(events?.value.events.length, 1);
});
