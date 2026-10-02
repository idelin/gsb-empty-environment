import { test } from "node:test";
import assert from "node:assert/strict";
import { IncrementalMaxFlow, type CutCertificate } from "../src/maxflow.ts";

/** Builds the acceptance-scenario network and returns the key edge id. */
function buildNetwork(): { mf: IncrementalMaxFlow; keyEdge: number } {
  const mf = new IncrementalMaxFlow("s", "t");
  mf.addEdge("s", "a", 5);
  mf.addEdge("s", "b", 5);
  const keyEdge = mf.addEdge("a", "t", 5);
  mf.addEdge("b", "t", 6);
  mf.addEdge("a", "b", 2);
  return { mf, keyEdge };
}

/** Independent validity check: capacity constraints + flow conservation. */
function assertFeasibleFlow(mf: IncrementalMaxFlow): void {
  const net = new Map<string, number>();
  for (const e of mf.snapshot()) {
    assert.ok(e.flow >= 0, `negative flow on ${e.u}->${e.v}`);
    assert.ok(e.flow <= e.capacity, `flow exceeds capacity on ${e.u}->${e.v}`);
    net.set(e.u, (net.get(e.u) ?? 0) - e.flow);
    net.set(e.v, (net.get(e.v) ?? 0) + e.flow);
  }
  for (const [node, balance] of net) {
    if (node !== mf.source && node !== mf.sink) {
      assert.equal(balance, 0, `flow not conserved at ${node}`);
    }
  }
  assert.equal(net.get(mf.source)!, -net.get(mf.sink)!);
}

function assertValidCertificate(cert: CutCertificate): void {
  assert.ok(cert.sourceSide.includes("s"));
  assert.ok(cert.sinkSide.includes("t"));
  const cutCapacity = cert.cutEdges.reduce((sum, e) => sum + e.capacity, 0);
  assert.equal(cert.cutCapacity, cutCapacity);
  assert.equal(cert.maxFlow, cert.cutCapacity, "max-flow min-cut duality");
  for (const e of cert.cutEdges) {
    assert.ok(cert.sourceSide.includes(e.u), `cut edge tail ${e.u} must be in S`);
    assert.ok(cert.sinkSide.includes(e.v), `cut edge head ${e.v} must be in T`);
  }
}

test("initial max flow of the network is 10", () => {
  const { mf } = buildNetwork();
  const cert = mf.computeMaxFlow();
  assert.equal(cert.maxFlow, 10);
  assert.equal(cert.cutCapacity, 10);
  assertValidCertificate(cert);
  assertFeasibleFlow(mf);
});

test("reducing key edge 5 -> 2 withdraws 3 units and readjusts flow to 8", () => {
  const { mf, keyEdge } = buildNetwork();
  mf.computeMaxFlow();

  const report = mf.updateCapacities([{ edge: keyEdge, capacity: 2 }]);

  assert.equal(report.withdrawn, 3, "3 units withdrawn along reverse tracks");
  assert.equal(report.reaugmented, 1, "1 unit recovered via alternative path");
  assert.equal(report.maxFlow, 8);

  const cert = report.certificate;
  assertValidCertificate(cert);
  assertFeasibleFlow(mf);
  assert.deepEqual([...cert.sourceSide].sort(), ["a", "b", "s"]);
  assert.deepEqual([...cert.sinkSide].sort(), ["t"]);
  const cutSet = cert.cutEdges.map((e) => `${e.u}->${e.v}:${e.capacity}`).sort();
  assert.deepEqual(cutSet, ["a->t:2", "b->t:6"]);
});

test("capacity decrease that stays feasible does not withdraw flow", () => {
  const { mf } = buildNetwork();
  mf.computeMaxFlow();
  const btEdge = mf.snapshot().find((e) => e.u === "b" && e.v === "t")!.id;

  const report = mf.updateCapacities([{ edge: btEdge, capacity: 5 }]);

  assert.equal(report.withdrawn, 0);
  assert.equal(report.maxFlow, 10);
  assertFeasibleFlow(mf);
});

test("capacity increase re-augments flow along newly available capacity", () => {
  const { mf } = buildNetwork();
  mf.computeMaxFlow();
  const saEdge = mf.snapshot().find((e) => e.u === "s" && e.v === "a")!.id;

  const report = mf.updateCapacities([{ edge: saEdge, capacity: 8 }]);

  assert.equal(report.withdrawn, 0);
  assert.equal(report.maxFlow, 11);
  assertValidCertificate(report.certificate);
  assertFeasibleFlow(mf);
});

test("batch of mixed increases and decreases is applied atomically", () => {
  const { mf, keyEdge } = buildNetwork();
  mf.computeMaxFlow();
  const sbEdge = mf.snapshot().find((e) => e.u === "s" && e.v === "b")!.id;

  const report = mf.updateCapacities([
    { edge: keyEdge, capacity: 2 }, // a->t: 5 -> 2 (withdraw 3)
    { edge: sbEdge, capacity: 9 }, // s->b: 5 -> 9 (more supply)
  ]);

  assert.equal(report.withdrawn, 3);
  // s->b now 9, b->t 6 caps the b side; a->t 2 + b->t 6 = 8 total
  assert.equal(report.maxFlow, 8);
  assertValidCertificate(report.certificate);
  assertFeasibleFlow(mf);
});

test("sequential updates stay consistent with a from-scratch recomputation", () => {
  const { mf, keyEdge } = buildNetwork();
  mf.computeMaxFlow();
  mf.updateCapacities([{ edge: keyEdge, capacity: 2 }]);
  const report = mf.updateCapacities([{ edge: keyEdge, capacity: 4 }]);

  // reference: same network rebuilt from scratch with a->t = 4
  const ref = new IncrementalMaxFlow("s", "t");
  ref.addEdge("s", "a", 5);
  ref.addEdge("s", "b", 5);
  ref.addEdge("a", "t", 4);
  ref.addEdge("b", "t", 6);
  ref.addEdge("a", "b", 2);
  const expected = ref.computeMaxFlow().maxFlow;

  assert.equal(report.maxFlow, expected);
  assertValidCertificate(report.certificate);
  assertFeasibleFlow(mf);
});

test("rejects invalid capacities and unknown edges", () => {
  const { mf, keyEdge } = buildNetwork();
  mf.computeMaxFlow();
  assert.throws(() => mf.updateCapacities([{ edge: keyEdge, capacity: -1 }]));
  assert.throws(() => mf.updateCapacities([{ edge: keyEdge, capacity: 1.5 }]));
  assert.throws(() => mf.updateCapacities([{ edge: 999, capacity: 1 }]));
  assert.throws(() => mf.addEdge("s", "t", -3));
});
