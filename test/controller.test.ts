import { test } from "node:test";
import assert from "node:assert/strict";
import { DualRingController } from "../src/controller.ts";
import { HashRing, type RingNode } from "../src/hash-ring.ts";

function makeNodes(count: number, zones = 3): RingNode[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `node-${i}`,
    zone: `zone-${i % zones}`,
  }));
}

function sampleKeys(count: number): string[] {
  return Array.from({ length: count }, (_, i) => `user:${i}`);
}

test("active ring serves all traffic while standby syncs in the background", () => {
  const controller = new DualRingController(makeNodes(9));
  const keys = sampleKeys(20_000);
  const baseline = keys.map((k) => controller.route(k));

  controller.syncStandby([{ type: "remove", nodeId: "node-4" }]);
  assert.equal(controller.standbyReady, false);

  // Active traffic is completely undisturbed by the standby-side change.
  for (let i = 0; i < keys.length; i++) {
    assert.equal(controller.route(keys[i]), baseline[i]);
  }
  assert.ok(controller.activeNodeIds().includes("node-4"));
});

test("prewarm pre-computes the migration impact of a scale-in", () => {
  const controller = new DualRingController(makeNodes(9));
  const keys = sampleKeys(50_000);
  controller.syncStandby([
    { type: "remove", nodeId: "node-4" },
    { type: "remove", nodeId: "node-7" },
  ]);
  const plan = controller.prewarm(keys);

  assert.deepEqual(plan.removedNodes.sort(), ["node-4", "node-7"]);
  assert.equal(plan.addedNodes.length, 0);
  assert.equal(plan.sampledKeys, keys.length);
  assert.ok(plan.movedKeys > 0);
  // Every moved key must have belonged to a removed node before the flip.
  assert.ok(plan.keyMoveRatio > 0.1 && plan.keyMoveRatio < 0.35);
  assert.ok(plan.vnodeMoveRatio > 0 && plan.vnodeMoveRatio < 0.5);
  assert.equal(controller.standbyReady, true);
});

test("atomic flip cuts over in milliseconds with zero transient misrouting", () => {
  const nodes = makeNodes(9);
  const controller = new DualRingController(nodes);
  const keys = sampleKeys(100_000);

  // Expected routing tables for both ring versions, computed independently.
  const oldRing = new HashRing(nodes);
  const newRing = new HashRing(nodes.filter((n) => n.id !== "node-4"));
  const validOwners = new Set([...oldRing.nodeIds(), ...newRing.nodeIds()]);

  controller.syncStandby([{ type: "remove", nodeId: "node-4" }]);
  controller.prewarm(keys);

  // Interleave heavy traffic with the flip: every single lookup must resolve
  // to a node that is valid in one of the two committed ring versions.
  let misrouted = 0;
  let flipResult: ReturnType<DualRingController["flip"]> | null = null;
  for (let i = 0; i < keys.length; i++) {
    if (i === Math.floor(keys.length / 2)) flipResult = controller.flip();
    const owner = controller.route(keys[i]);
    const ring = flipResult === null ? oldRing : newRing;
    if (owner !== ring.lookup(keys[i]) || !validOwners.has(owner)) misrouted++;
  }

  assert.ok(flipResult !== null);
  assert.equal(misrouted, 0, "zero instantaneous routing errors during flip");
  assert.ok(
    flipResult.durationMs < 10,
    `flip took ${flipResult.durationMs}ms, expected millisecond-level`,
  );
  assert.equal(controller.flipCount, 1);

  // After cutover, traffic follows the new topology exactly.
  for (const key of keys) {
    const owner = controller.route(key);
    assert.notEqual(owner, "node-4");
    assert.equal(owner, newRing.lookup(key));
  }
});

test("flip is rejected while the standby ring is dirty", () => {
  const controller = new DualRingController(makeNodes(6));
  controller.syncStandby([{ type: "remove", nodeId: "node-1" }]);
  assert.throws(() => controller.flip(), /not warmed/);
  controller.prewarm();
  assert.doesNotThrow(() => controller.flip());
});

test("failover performs the same one-click atomic switch", () => {
  const controller = new DualRingController(makeNodes(6));
  controller.syncStandby([{ type: "add", node: { id: "node-9", zone: "zone-0" } }]);
  controller.prewarm();
  const result = controller.failover();
  assert.equal(result.flipCount, 1);
  assert.ok(controller.activeNodeIds().includes("node-9"));
});

test("controller supports repeated sync/prewarm/flip cycles", () => {
  const controller = new DualRingController(makeNodes(6));
  controller.syncStandby([{ type: "remove", nodeId: "node-0" }]);
  controller.prewarm();
  controller.flip();
  assert.ok(!controller.activeNodeIds().includes("node-0"));

  controller.syncStandby([{ type: "add", node: { id: "node-0", zone: "zone-0" } }]);
  const plan = controller.prewarm(sampleKeys(10_000));
  assert.deepEqual(plan.addedNodes, ["node-0"]);
  controller.flip();
  assert.ok(controller.activeNodeIds().includes("node-0"));
  assert.equal(controller.flipCount, 2);
});

test("emptying the standby ring is rejected", () => {
  const controller = new DualRingController([{ id: "solo" }]);
  assert.throws(
    () => controller.syncStandby([{ type: "remove", nodeId: "solo" }]),
    /empty/,
  );
});
