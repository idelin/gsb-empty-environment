import { test } from "node:test";
import assert from "node:assert/strict";
import { makeHarness, invite, consent, migration } from "./helpers.ts";
import { CasReceiptDirectory, InMemoryCasStore } from "../src/index.ts";

test("a release signed for an old generation after takeover cannot re-open the session", async () => {
  const h = makeHarness();
  const invitation = invite(h, "s-gen", "g1");
  await h.nodeA.submitFact(
    "s-gen",
    h.sig.signFact(h.alice, "invitation", invitation, { signedAt: h.clock.now }),
  );
  await h.nodeA.submitFact("s-gen", consent(h, invitation, h.alice));
  await h.nodeA.submitFact("s-gen", consent(h, invitation, h.bob));
  await h.nodeA.registerConnection("s-gen", h.bob.identity, "bob", "addr-1", 1);

  const first = migration(h, invitation, h.bob, h.alice, 1, 2, "node-A", "node-B", "g-1");
  for (const piece of [first.release, first.takeover, first.approval]) {
    assert.equal((await h.nodeA.submitFact("s-gen", piece)).ok, true);
  }

  const state = await h.nodeA.getState("s-gen");
  assert.equal(state.parties[h.bob.identity].generation, 2);

  const stale = migration(h, invitation, h.bob, h.alice, 1, 2, "node-A", "node-X", "g-old");
  const result = await h.nodeA.submitFact("s-gen", stale.release);
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.reason, "stale-generation");
  }
});

test("receiver receipts bind the recipient identity and refuse re-routing", async () => {
  const h = makeHarness();
  const directory = new CasReceiptDirectory(new InMemoryCasStore());
  const base = {
    answerId: "bind-1",
    sessionId: "s-bind",
    invitationDigest: "digest",
    recipient: h.bob.identity,
    generation: 3,
    node: "node-B",
  };
  await directory.claim(base);
  await assert.rejects(
    directory.claim({ ...base, recipient: h.carol.identity, generation: 4, node: "node-C" }),
    /already claimed by/,
  );
  const stored = await directory.getByAnswer("bind-1");
  assert.equal(stored?.recipient, h.bob.identity);
  assert.equal(stored?.generation, 3);
});

test("migration approval must be signed by the counterparty, not the migrating party", async () => {
  const h = makeHarness();
  const invitation = invite(h, "s-approval", "g1");
  await h.nodeA.submitFact(
    "s-approval",
    h.sig.signFact(h.alice, "invitation", invitation, { signedAt: h.clock.now }),
  );
  await h.nodeA.submitFact("s-approval", consent(h, invitation, h.alice));
  await h.nodeA.submitFact("s-approval", consent(h, invitation, h.bob));
  await h.nodeA.registerConnection("s-approval", h.bob.identity, "bob", "addr", 1);

  const mig = migration(h, invitation, h.bob, h.alice, 1, 2, "node-A", "node-B", "self-approval");
  const selfSignedApproval = h.sig.signFact(
    h.bob,
    "migration-approval",
    mig.approval.payload,
    { signedAt: h.clock.now, factId: "bogus-approval" },
  );
  const result = await h.nodeA.submitFact("s-approval", selfSignedApproval);
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.reason, "wrong-signer");
  }
});
