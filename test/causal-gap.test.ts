import { test } from "node:test";
import assert from "node:assert/strict";
import { CausalGapHandler, type CausalMessage } from "../src/causal-gap.ts";

function msg(id: string, seq: number, predecessors: string[] = []): CausalMessage {
  return { id, seq, predecessors };
}

test("delivers messages whose predecessors are already delivered", () => {
  const h = new CausalGapHandler();
  assert.equal(h.receive(msg("p1", 1)).status, "delivered");
  assert.equal(h.receive(msg("m1", 2, ["p1"])).status, "delivered");
});

test("buffers out-of-order successors and cascades on predecessor arrival", () => {
  const h = new CausalGapHandler();
  assert.equal(h.receive(msg("m1", 2, ["p1"])).status, "buffered");
  assert.equal(h.receive(msg("m2", 3, ["m1"])).status, "buffered");
  h.receive(msg("p1", 1));
  assert.ok(h.isDelivered("m1"));
  assert.ok(h.isDelivered("m2"));
});

test("successor of an expired predecessor hangs and is never skipped without an installed boundary", () => {
  const h = new CausalGapHandler();
  h.receive(msg("m1", 2, ["p1"]));
  h.expire("p1");

  // A boundary snapshot exists but has not been installed: no skipping.
  const boundary = h.createResetBoundary();
  assert.equal(boundary.gapProofs.length, 1);
  assert.equal(boundary.gapProofs[0].predecessorId, "p1");
  assert.deepEqual(boundary.gapProofs[0].affectedPending, ["m1"]);
  assert.ok(h.isPending("m1"), "must not skip before boundary install");

  const affected = h.getAffectedMessages().find((a) => a.messageId === "m1");
  assert.equal(affected?.status, "awaiting-boundary-install");
});

test("installing the boundary releases old successors via gap proofs", () => {
  const h = new CausalGapHandler();
  h.receive(msg("m1", 2, ["p1"]));
  h.expire("p1", "retention-expired");
  const boundary = h.createResetBoundary();

  const result = h.installBoundary(boundary);
  assert.deepEqual(result.released, ["m1"]);
  assert.deepEqual(result.stillPending, []);
  assert.ok(h.isDelivered("m1"));

  const affected = h.getAffectedMessages().find((a) => a.messageId === "m1");
  assert.equal(affected?.status, "released-by-boundary");
  assert.deepEqual(affected?.expiredPredecessors, ["p1"]);
});

test("reset interleaved with new events: new dependencies are not skipped", () => {
  const h = new CausalGapHandler();
  // Old message m1 hangs on expired predecessor p1.
  h.receive(msg("m1", 2, ["p1"]));
  h.expire("p1");
  const boundary = h.createResetBoundary();

  // New events arrive after the boundary snapshot but before install.
  h.receive(msg("m2", 3, ["p2"])); // depends on a merely-missing predecessor
  h.receive(msg("m3", 4, ["p1"])); // depends on the expired p1, but is NEW

  const result = h.installBoundary(boundary);
  assert.deepEqual(result.released, ["m1"], "only old messages may be released");
  assert.deepEqual(result.stillPending.sort(), ["m2", "m3"]);

  // m2 waits for p2 normally; delivering p2 releases it.
  assert.ok(h.isPending("m2"));
  h.receive(msg("p2", 5));
  assert.ok(h.isDelivered("m2"));

  // m3 is newer than the horizon: the boundary must not skip p1 for it.
  assert.ok(h.isPending("m3"), "new dependency on expired predecessor must not be skipped");
  const affected = h.getAffectedMessages().find((a) => a.messageId === "m3");
  assert.equal(affected?.status, "blocked-new-dependency");

  // A newer boundary created after m3's horizon can cover it.
  const boundary2 = h.createResetBoundary();
  const result2 = h.installBoundary(boundary2);
  assert.deepEqual(result2.released, ["m3"]);
});

test("old retransmitted message arriving after install may skip proven predecessors", () => {
  const h = new CausalGapHandler();
  h.receive(msg("m1", 2, ["p1"]));
  h.expire("p1");
  h.installBoundary(h.createResetBoundary());

  // Retransmission of an old message (seq <= horizon) arrives late.
  const r = h.receive(msg("m4", 1, ["p1"]));
  assert.equal(r.status, "delivered-with-skipped-predecessors");
  assert.deepEqual(r.skippedPredecessors, ["p1"]);
});

test("predecessor expired after boundary creation is not covered by the stale boundary", () => {
  const h = new CausalGapHandler();
  h.receive(msg("m1", 2, ["p1"]));
  h.expire("p1");
  const boundary = h.createResetBoundary();

  // p2 expires only after the snapshot; the stale boundary must not cover it.
  h.receive(msg("m2", 3, ["p2"]));
  h.expire("p2");

  const result = h.installBoundary(boundary);
  assert.deepEqual(result.released, ["m1"]);
  assert.ok(h.isPending("m2"));

  const result2 = h.installBoundary(h.createResetBoundary());
  assert.deepEqual(result2.released, ["m2"]);
});

test("recovery plan lists affected messages and ordered steps", () => {
  const h = new CausalGapHandler();
  h.receive(msg("m1", 2, ["p1"]));
  h.receive(msg("m2", 3, ["p2"]));
  h.expire("p1");

  const plan = h.getRecoveryPlan();
  assert.ok(plan.some((s) => s.includes("createResetBoundary") && s.includes("p1")));
  assert.ok(plan.some((s) => s.includes("installBoundary") && s.includes("m1")));
  assert.ok(plan.some((s) => s.includes("p2") && s.includes("m2")));

  const affected = h.getAffectedMessages();
  assert.equal(affected.find((a) => a.messageId === "m1")?.status, "awaiting-boundary-install");
  assert.equal(affected.find((a) => a.messageId === "m2")?.status, "awaiting-predecessor");
});

test("empty recovery plan when nothing is pending", () => {
  const h = new CausalGapHandler();
  h.receive(msg("p1", 1));
  assert.deepEqual(h.getRecoveryPlan(), ["no pending gaps; nothing to recover"]);
});
