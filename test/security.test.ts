import { test } from "node:test";
import assert from "node:assert/strict";
import { makeHarness, invite, consent, answer } from "./helpers.ts";

test("a tampered signature is rejected", async () => {
  const h = makeHarness();
  const invitation = invite(h, "s-sig", "g1");
  const signed = h.sig.signFact(h.alice, "invitation", invitation, { signedAt: h.clock.now });
  const tampered = { ...signed, signature: `X${signed.signature.slice(1)}` };
  const result = await h.nodeA.submitFact("s-sig", tampered);
  assert.equal(result.ok, false);
  assert.equal(result.ok === false && result.reason, "bad-signature");
});

test("an answer signed by the wrong key is rejected even for a valid party", async () => {
  const h = makeHarness();
  const invitation = invite(h, "s-wrongkey", "g1");
  await h.nodeA.submitFact(
    "s-wrongkey",
    h.sig.signFact(h.alice, "invitation", invitation, { signedAt: h.clock.now }),
  );
  await h.nodeA.submitFact("s-wrongkey", consent(h, invitation, h.alice));
  await h.nodeA.submitFact("s-wrongkey", consent(h, invitation, h.bob));

  const forged = answer(h, invitation, "forged", h.carol, h.bob);
  const result = await h.nodeA.submitFact("s-wrongkey", forged);
  assert.equal(result.ok, false);
  assert.equal(result.ok === false && result.reason, "unknown-party");
});

test("consent scope without relay-answer blocks forwarding", async () => {
  const h = makeHarness();
  const invitation = invite(h, "s-scope", "g1");
  await h.nodeA.submitFact(
    "s-scope",
    h.sig.signFact(h.alice, "invitation", invitation, { signedAt: h.clock.now }),
  );
  await h.nodeA.submitFact("s-scope", consent(h, invitation, h.alice, ["migrate-connection"]));
  await h.nodeA.submitFact("s-scope", consent(h, invitation, h.bob));

  const blocked = await h.nodeA.submitFact(
    "s-scope",
    answer(h, invitation, "a1", h.alice, h.bob),
  );
  assert.equal(blocked.ok, false);
  assert.equal(blocked.ok === false && blocked.reason, "scope-action-denied");
});

test("expired consent is rejected at submission time", async () => {
  const h = makeHarness();
  const invitation = invite(h, "s-expired", "g1");
  await h.nodeA.submitFact(
    "s-expired",
    h.sig.signFact(h.alice, "invitation", invitation, { signedAt: h.clock.now }),
  );
  const stale = consent(h, invitation, h.alice);
  h.clock.now += 1_000_000;
  const result = await h.nodeA.submitFact("s-expired", stale);
  assert.equal(result.ok, false);
  assert.equal(result.ok === false && result.reason, "consent-expired");
});

test("display name and transport address reuse never changes the recipient", async () => {
  const h = makeHarness();
  const invitation = invite(h, "s-identity", "g1");
  await h.nodeA.submitFact(
    "s-identity",
    h.sig.signFact(h.alice, "invitation", invitation, { signedAt: h.clock.now }),
  );
  await h.nodeA.submitFact("s-identity", consent(h, invitation, h.alice));
  await h.nodeA.submitFact("s-identity", consent(h, invitation, h.bob));
  await h.nodeA.registerConnection("s-identity", h.bob.identity, "shared-name", "shared-addr", 1);
  await h.nodeA.submitFact("s-identity", answer(h, invitation, "a1", h.alice, h.bob));

  await h.nodeA.registerConnection("s-identity", h.carol.identity, "shared-name", "shared-addr", 1);
  const verdict = await h.nodeA.queryVerdict("s-identity", "a1");
  assert.equal(verdict.status, "deliverable");
  if (verdict.status === "deliverable") {
    assert.equal(verdict.to, h.bob.identity);
  }
});

test("a reused session id does not inherit the previous invitation's consent", async () => {
  const h = makeHarness();
  const first = invite(h, "reused-id", "g1");
  await h.nodeA.submitFact(
    "reused-id",
    h.sig.signFact(h.alice, "invitation", first, { signedAt: h.clock.now }),
  );
  const second = invite(h, "reused-id", "g2");
  const result = await h.nodeA.submitFact(
    "reused-id",
    h.sig.signFact(h.alice, "invitation", second, { signedAt: h.clock.now }),
  );
  assert.equal(result.ok, false);
  assert.equal(result.ok === false && result.reason, "session-id-conflict");
});
