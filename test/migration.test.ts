import { test } from "node:test";
import assert from "node:assert/strict";
import { makeHarness, invite, consent, migration, permutations, replayAll } from "./helpers.ts";
import type { SessionEvent } from "../src/index.ts";

test("the three migration facts complete in every arrival order", async () => {
  for (const order of permutations(["release", "takeover", "approval"] as const)) {
    const h = makeHarness();
    const invitation = invite(h, "s-mig", "g1");
    await h.nodeA.submitFact("s-mig", h.sig.signFact(h.alice, "invitation", invitation, { signedAt: h.clock.now }));
    await h.nodeA.submitFact("s-mig", consent(h, invitation, h.alice));
    await h.nodeA.submitFact("s-mig", consent(h, invitation, h.bob));
    await h.nodeA.registerConnection("s-mig", h.bob.identity, "bob", "addr", 1);
    const mig = migration(h, invitation, h.bob, h.alice, 1, 2, "node-A", "node-B", "mig-x");

    for (const piece of order) {
      const partial = await h.nodeA.queryVerdict("s-mig", "nonexistent");
      assert.equal(partial.status, "blocked");
      await h.nodeA.submitFact("s-mig", mig[piece]);
    }

    const state = await h.nodeA.getState("s-mig");
    assert.equal(state.migrations["mig-x"].completed, true);
    assert.equal(state.parties[h.bob.identity].generation, 2);
    assert.equal(state.parties[h.bob.identity].node, "node-B");
  }
});

test("facts from a different migration transaction never merge", async () => {
  const h = makeHarness();
  const invitation = invite(h, "s-split", "g1");
  await h.nodeA.submitFact("s-split", h.sig.signFact(h.alice, "invitation", invitation, { signedAt: h.clock.now }));
  await h.nodeA.submitFact("s-split", consent(h, invitation, h.alice));
  await h.nodeA.submitFact("s-split", consent(h, invitation, h.bob));
  await h.nodeA.registerConnection("s-split", h.bob.identity, "bob", "addr", 1);

  const first = migration(h, invitation, h.bob, h.alice, 1, 2, "node-A", "node-B", "tx-1");
  const second = migration(h, invitation, h.bob, h.alice, 1, 2, "node-A", "node-C", "tx-2");

  await h.nodeA.submitFact("s-split", first.release);
  await h.nodeA.submitFact("s-split", first.takeover);
  // Approval belongs to a different transaction: tx-1 must stay incomplete.
  const rejected = await h.nodeA.submitFact("s-split", second.approval);
  assert.equal(rejected.ok, false);

  const state = await h.nodeA.getState("s-split");
  assert.equal(state.migrations["tx-1"].completed, false);
  assert.equal(state.migrations["tx-1"].approval, null);
});

test("two concurrent migrations for one party cannot both be pending", async () => {
  const h = makeHarness();
  const invitation = invite(h, "s-pending", "g1");
  await h.nodeA.submitFact("s-pending", h.sig.signFact(h.alice, "invitation", invitation, { signedAt: h.clock.now }));
  await h.nodeA.submitFact("s-pending", consent(h, invitation, h.alice));
  await h.nodeA.submitFact("s-pending", consent(h, invitation, h.bob));
  await h.nodeA.registerConnection("s-pending", h.bob.identity, "bob", "addr", 1);

  const one = migration(h, invitation, h.bob, h.alice, 1, 2, "node-A", "node-B", "p1");
  const two = migration(h, invitation, h.bob, h.alice, 1, 2, "node-A", "node-C", "p2");
  assert.ok(await h.nodeA.submitFact("s-pending", one.release));
  const blocked = await h.nodeA.submitFact("s-pending", two.release);
  assert.equal(blocked.ok, false);
  assert.equal(blocked.ok === false && blocked.reason, "migration-already-pending");
});

test("stale release against the wrong generation is rejected", async () => {
  const h = makeHarness();
  const invitation = invite(h, "s-stale", "g1");
  await h.nodeA.submitFact("s-stale", h.sig.signFact(h.alice, "invitation", invitation, { signedAt: h.clock.now }));
  await h.nodeA.submitFact("s-stale", consent(h, invitation, h.alice));
  await h.nodeA.submitFact("s-stale", consent(h, invitation, h.bob));
  await h.nodeA.registerConnection("s-stale", h.bob.identity, "bob", "addr", 7);

  const mig = migration(h, invitation, h.bob, h.alice, 1, 2, "node-A", "node-B", "stale");
  const result = await h.nodeA.submitFact("s-stale", mig.release);
  assert.equal(result.ok, false);
  assert.equal(result.ok === false && result.reason, "stale-generation");
});

test("replaying the same event stream twice yields identical state", () => {
  const events: SessionEvent[] = [];
  const first = replayAll(events);
  const second = replayAll(events);
  assert.deepEqual(first, second);
});
