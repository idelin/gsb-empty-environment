import test from "node:test";
import assert from "node:assert/strict";
import { CausalGapHandler } from "../src/index.ts";
import type { CausalMessage } from "../src/index.ts";

function msg(id: string, deps: string[], clock: number): CausalMessage<string> {
  return { id, deps, clock, payload: `payload-${id}` };
}

test("delivers immediately when all predecessors are delivered", () => {
  const h = new CausalGapHandler<string>();
  assert.equal(h.receive(msg("a", [], 1)).status, "delivered");
  assert.equal(h.receive(msg("b", ["a"], 2)).status, "delivered");
  assert.deepEqual(h.delivered().map((m) => m.id), ["a", "b"]);
});

test("buffers a successor whose predecessor has not arrived", () => {
  const h = new CausalGapHandler<string>();
  const r = h.receive(msg("b", ["a"], 2));
  assert.equal(r.status, "pending");
  assert.deepEqual(r.missing, ["a"]);
  assert.equal(h.pendingCount(), 1);
  assert.equal(h.receive(msg("a", [], 1)).status, "delivered");
  assert.deepEqual(h.delivered().map((m) => m.id), ["a", "b"]);
  assert.equal(h.pendingCount(), 0);
});

test("successor of an expired predecessor hangs until a boundary is installed", () => {
  const h = new CausalGapHandler<string>();
  h.expire("a"); // a expired before it ever arrived at this node
  assert.equal(h.receive(msg("b", ["a"], 2)).status, "pending");

  const before = h.audit();
  assert.equal(before.boundaryId, null);
  assert.equal(before.steps[0].action, "still-pending");
  assert.match(before.steps[0].detail, /no reset boundary installed/);

  const report = h.installBoundary(h.createResetBoundary());
  assert.equal(h.isDelivered("b"), true);
  const step = report.steps.find((s) => s.messageId === "b");
  assert.equal(step?.action, "released");
  assert.deepEqual(step?.skippedDeps, ["a"]);
});

test("never skips a predecessor without a gap proof, even with a boundary", () => {
  const h = new CausalGapHandler<string>();
  h.receive(msg("b", ["a"], 2));
  h.installBoundary(h.createResetBoundary());
  assert.equal(h.isDelivered("b"), false);
  const audit = h.audit();
  assert.equal(audit.steps[0].action, "still-pending");
  assert.deepEqual(audit.steps[0].remainingDeps, ["a"]);
});

test("reset interleaved with new events: post-watermark dependencies are not skipped", () => {
  const h = new CausalGapHandler<string>();
  h.expire("a");
  h.receive(msg("b", ["a"], 2));

  const boundary = h.createResetBoundary();
  const watermark = boundary.watermark;

  // New event stream interleaves with the reset, after the watermark.
  // c is a brand-new post-watermark message that has not arrived here; d and
  // e depend on it, and it expires only AFTER the boundary was taken.
  h.receive(msg("d", ["c"], watermark + 3));
  h.receive(msg("e", ["c", "x"], watermark + 4));
  h.expire("c", "expired", watermark + 2);

  const report = h.installBoundary(boundary);

  // Old gap recovered.
  assert.equal(h.isDelivered("b"), true);
  assert.deepEqual(
    report.steps.find((s) => s.messageId === "b")?.skippedDeps,
    ["a"],
  );

  // New dependency c: expired, but its proof postdates the watermark, so the
  // installed boundary must not skip it.
  assert.equal(h.isDelivered("d"), false);
  assert.deepEqual(h.pendingState("d")?.missing, ["c"]);

  // x is merely missing (no proof at all) and must keep e pending too.
  assert.equal(h.isDelivered("e"), false);
  assert.deepEqual(h.pendingState("e")?.missing.sort(), ["c", "x"]);

  // A fresh boundary that includes c's proof releases d, and x arriving
  // releases e through the normal cascade.
  h.installBoundary(h.createResetBoundary());
  assert.equal(h.isDelivered("d"), true);
  h.receive(msg("x", [], watermark + 5));
  assert.equal(h.isDelivered("e"), true);
});

test("a dependency created after the watermark is never skippable", () => {
  const h = new CausalGapHandler<string>();
  h.expire("a");
  h.receive(msg("b", ["a"], 2));
  const boundary = h.createResetBoundary();

  // c is a brand-new event from after the watermark; b2 depends on the old
  // gap (a) and the new event (c). Only a may be skipped.
  h.receive(msg("b2", ["a", "c"], boundary.watermark + 2));
  const report = h.installBoundary(boundary);

  assert.equal(h.isDelivered("b"), true);
  assert.equal(h.isDelivered("b2"), false);
  const step = report.steps.find((s) => s.messageId === "b2");
  assert.equal(step?.action, "still-pending");
  assert.deepEqual(step?.skippedDeps, ["a"]);
  assert.deepEqual(step?.remainingDeps, ["c"]);

  h.receive(msg("c", [], boundary.watermark + 1));
  assert.equal(h.isDelivered("b2"), true);
});

test("rejects invalid boundary snapshots", () => {
  const h = new CausalGapHandler<string>();
  h.expire("a");
  const boundary = h.createResetBoundary();

  const futureProof = {
    ...boundary,
    gapProofs: [{ messageId: "z", reason: "expired" as const, clock: 1, removedAt: boundary.watermark + 10 }],
  };
  assert.throws(
    () => h.installBoundary(futureProof),
    /newer than the boundary watermark/,
  );

  const postWatermarkClock = {
    ...boundary,
    gapProofs: [{ messageId: "z", reason: "expired" as const, clock: boundary.watermark, removedAt: boundary.watermark }],
  };
  assert.throws(
    () => h.installBoundary(postWatermarkClock),
    /not older than the boundary watermark/,
  );

  const duplicate = {
    ...boundary,
    gapProofs: [...boundary.gapProofs, ...boundary.gapProofs],
  };
  assert.throws(() => h.installBoundary(duplicate), /duplicate gap proof/);

  assert.equal(h.installBoundary(boundary).boundaryId, boundary.id);
});

test("rejects expiring a pending successor and non-causal clocks", () => {
  const h = new CausalGapHandler<string>();
  h.receive(msg("b", ["a"], 2));
  assert.throws(() => h.expire("b"), /pending successor/);
  assert.throws(() => h.receive(msg("c", ["b"], 2)), /greater than its dep/);
});

test("released message cascades to its own pending successors", () => {
  const h = new CausalGapHandler<string>();
  h.expire("a");
  h.receive(msg("b", ["a"], 2));
  h.receive(msg("c", ["b"], 3));

  const report = h.installBoundary(h.createResetBoundary());
  assert.deepEqual(
    report.steps.map((s) => `${s.messageId}:${s.action}`),
    ["b:released", "c:delivered"],
  );
  assert.deepEqual(h.delivered().map((m) => m.id), ["b", "c"]);
});

test("audit lists every affected message with its recovery step", () => {
  const h = new CausalGapHandler<string>();
  h.expire("a");
  h.receive(msg("b", ["a"], 2)); // recoverable via gap proof
  h.receive(msg("c", ["a", "y"], 3)); // y has no proof: stays pending
  h.receive(msg("d", ["z"], 4)); // no proof at all: stays pending

  const report = h.installBoundary(h.createResetBoundary());
  assert.equal(report.boundaryId, h.audit().boundaryId);

  const byId = new Map(report.steps.map((s) => [s.messageId, s]));
  assert.equal(byId.get("b")?.action, "released");
  assert.equal(byId.get("c")?.action, "still-pending");
  assert.deepEqual(byId.get("c")?.remainingDeps, ["y"]);
  assert.equal(byId.get("d"), undefined); // untouched: nothing to skip

  const after = h.audit();
  assert.deepEqual(
    after.steps.map((s) => s.messageId).sort(),
    ["c", "d"],
  );
});

test("duplicate receives are ignored", () => {
  const h = new CausalGapHandler<string>();
  h.receive(msg("a", [], 1));
  assert.equal(h.receive(msg("a", [], 1)).status, "duplicate");
  h.receive(msg("b", ["x"], 2));
  assert.equal(h.receive(msg("b", ["x"], 2)).status, "duplicate");
  assert.equal(h.pendingCount(), 1);
});
