import { test } from "node:test";
import assert from "node:assert/strict";
import { once } from "node:events";
import { ClusterNode } from "../src/node.ts";
import { NodeDrainingError, NoReadyNodeError, UnknownNodeError } from "../src/errors.ts";
import { NodeState } from "../src/types.ts";

const QUIESCENT_MS = 80;

function makeCluster() {
  const a = new ClusterNode("A", { quiescentMs: QUIESCENT_MS, rpcLatencyMs: 30 });
  const b = new ClusterNode("B", { quiescentMs: QUIESCENT_MS, rpcLatencyMs: 30 });
  const c = new ClusterNode("C", { quiescentMs: QUIESCENT_MS, rpcLatencyMs: 30 });
  a.connect([b, c]);
  b.connect([a, c]);
  c.connect([a, b]);
  return { a, b, c };
}

test("node initiates Leave: local state becomes Leaving and intent is broadcast", async () => {
  const { b } = makeCluster();
  const broadcast = once(b, "leave-broadcast");
  b.initiateLeave();
  const [intent] = await broadcast;
  assert.equal(b.selfState, NodeState.Leaving);
  assert.equal(intent.kind, "LeaveIntent");
  assert.equal(intent.nodeId, "B");
});

test("peers smoothly evict the leaving node from the ready route pool", () => {
  const { a, b, c } = makeCluster();
  assert.deepEqual(a.readyPool().sort(), ["A", "B", "C"]);
  b.initiateLeave();
  assert.equal(a.getRouteEntry("B")?.state, NodeState.Leaving);
  assert.equal(c.getRouteEntry("B")?.state, NodeState.Leaving);
  assert.deepEqual(a.readyPool().sort(), ["A", "C"]);
  assert.deepEqual(c.readyPool().sort(), ["A", "C"]);
  assert.ok(a.getRouteEntry("B") !== undefined);
});

test("new traffic to a Leaving node fails fast during quiescent period", async () => {
  const { a, b } = makeCluster();
  b.initiateLeave();
  const start = Date.now();
  await assert.rejects(
    a.sendRpc("B", { id: "r1", payload: "x" }),
    (err: Error) => err instanceof NodeDrainingError,
  );
  assert.ok(Date.now() - start < QUIESCENT_MS, "must fail fast, not hang until timeout");
});

test("in-flight requests drain and complete during quiescent period", async () => {
  const { a, b } = makeCluster();
  const inflight = a.sendRpc("B", { id: "inflight-1", payload: "x" });
  b.initiateLeave();
  const resp = await inflight;
  assert.equal(resp.ok, true);
  assert.equal(resp.from, "B");
});

test("route entry is physically destroyed after quiescent timer expires", async () => {
  const { a, b, c } = makeCluster();
  const removedOnA = once(a, "route-removed");
  const removedOnC = once(c, "route-removed");
  b.initiateLeave();
  const [[removedA], [removedC]] = await Promise.all([removedOnA, removedOnC]);
  assert.equal(removedA, "B");
  assert.equal(removedC, "B");
  assert.equal(a.getRouteEntry("B"), undefined);
  assert.equal(c.getRouteEntry("B"), undefined);
  await assert.rejects(
    a.sendRpc("B", { id: "r2", payload: "x" }),
    (err: Error) => err instanceof UnknownNodeError,
  );
});

test("duplicate LeaveIntent is idempotent and does not reset the quiescent timer", async () => {
  const { a, b } = makeCluster();
  b.initiateLeave();
  const timer1 = a.getRouteEntry("B")?.quiescentTimer;
  a.onLeaveIntent({ kind: "LeaveIntent", nodeId: "B", epoch: 2, sentAt: Date.now() });
  const timer2 = a.getRouteEntry("B")?.quiescentTimer;
  assert.equal(timer1, timer2);
  await once(a, "route-removed");
});

test("route pool exhaustion throws NoReadyNodeError", async () => {
  const { a, b, c } = makeCluster();
  b.initiateLeave();
  c.initiateLeave();
  await Promise.all([once(a, "route-removed"), once(a, "route-removed")]);
  assert.throws(() => a.route(), NoReadyNodeError);
});
