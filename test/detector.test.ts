import test from "node:test";
import assert from "node:assert/strict";
import {
  WaitGraph,
  findShortestCycleInEdges,
  type CycleEdge,
  type DetectorEvent,
} from "../src/index.ts";

function feed(graph: WaitGraph, events: DetectorEvent[]): void {
  graph.applyAll(events);
}

function begins(graph: WaitGraph, partition: string, txns: string[], fromVersion = 1): void {
  txns.forEach((txn, i) =>
    graph.apply({ kind: "txn-begin", partition, txn, version: fromVersion + i }),
  );
}

test("global cycle detected even when every partition is locally acyclic", () => {
  const g = new WaitGraph();
  feed(g, [
    { kind: "edge-upsert", partition: "p1", from: "T1", to: "T2", version: 1 },
    { kind: "edge-upsert", partition: "p2", from: "T2", to: "T3", version: 1 },
    { kind: "edge-upsert", partition: "p3", from: "T3", to: "T1", version: 1 },
  ]);

  // Each partition's local view is acyclic.
  for (const p of ["p1", "p2", "p3"]) {
    const local = g.liveEdges().filter((e) => e.partition === p);
    assert.equal(findShortestCycleInEdges(local), null, `${p} must be acyclic`);
  }

  const report = g.detect();
  assert.ok(report, "merged graph must contain a cycle");
  assert.equal(report.length, 3);
  assert.deepEqual(report.nodes, ["T1", "T2", "T3"]);
  assert.deepEqual(
    report.edges.map((e) => `${e.partition}:${e.from}->${e.to}@v${e.version}`),
    ["p1:T1->T2@v1", "p2:T2->T3@v1", "p3:T3->T1@v1"],
  );
});

test("out-of-order remove does not resurrect an old edge", () => {
  const g = new WaitGraph();
  // Partition p1 emits upsert@v1 then remove@v2; delivery swaps them.
  g.apply({ kind: "edge-remove", partition: "p1", from: "T1", to: "T2", version: 2 });
  const r = g.apply({ kind: "edge-upsert", partition: "p1", from: "T1", to: "T2", version: 1 });
  assert.deepEqual(r, { applied: false, reason: "stale-version" });
  assert.equal(g.liveEdges().length, 0, "tombstone must block the stale upsert");
  assert.equal(g.detect(), null);
});

test("out-of-order remove loses to a newer upsert", () => {
  const g = new WaitGraph();
  // p1 emits remove@v1 then upsert@v2; delivery swaps them.
  g.apply({ kind: "edge-upsert", partition: "p1", from: "T1", to: "T2", version: 2 });
  const r = g.apply({ kind: "edge-remove", partition: "p1", from: "T1", to: "T2", version: 1 });
  assert.deepEqual(r, { applied: false, reason: "stale-version" });
  assert.equal(g.liveEdges().length, 1, "newer upsert must survive the stale remove");
});

test("out-of-order txn-end does not resurrect a dead transaction", () => {
  const g = new WaitGraph();
  g.apply({ kind: "txn-end", partition: "p1", txn: "T1", version: 5 });
  const r = g.apply({ kind: "txn-begin", partition: "p1", txn: "T1", version: 2 });
  assert.deepEqual(r, { applied: false, reason: "stale-version" });
  assert.equal(g.isAlive("T1"), false);
});

test("shortest cycle wins and reports edge versions", () => {
  const g = new WaitGraph();
  feed(g, [
    // Long cycle: T1 -> T2 -> T3 -> T1 (length 3)
    { kind: "edge-upsert", partition: "p1", from: "T1", to: "T2", version: 1 },
    { kind: "edge-upsert", partition: "p2", from: "T2", to: "T3", version: 2 },
    { kind: "edge-upsert", partition: "p3", from: "T3", to: "T1", version: 3 },
    // Shortcut closing a 2-cycle: T1 -> T3, T3 -> T1
    { kind: "edge-upsert", partition: "p1", from: "T1", to: "T3", version: 4 },
  ]);
  const report = g.detect();
  assert.ok(report);
  assert.equal(report.length, 2);
  assert.deepEqual(report.nodes, ["T1", "T3"]);
  assert.deepEqual(
    report.edges.map((e) => e.version),
    [4, 3],
    "report must carry the version of every edge in the cycle",
  );
});

test("victim suggestion only targets transactions that still exist", () => {
  const g = new WaitGraph();
  begins(g, "p1", ["T1", "T2", "T3"]);
  feed(g, [
    { kind: "edge-upsert", partition: "p1", from: "T1", to: "T2", version: 10 },
    { kind: "edge-upsert", partition: "p2", from: "T2", to: "T3", version: 10 },
    { kind: "edge-upsert", partition: "p3", from: "T3", to: "T1", version: 10 },
  ]);
  const report = g.detect();
  assert.ok(report);

  // T1 commits before the detector acts; it must leave the candidate set.
  g.apply({ kind: "txn-end", partition: "p1", txn: "T1", version: 20 });
  const victim = g.suggestVictim(report);
  assert.ok(victim === "T2" || victim === "T3", `got ${victim}`);

  // All members gone: no suggestion at all.
  g.apply({ kind: "txn-end", partition: "p1", txn: "T2", version: 21 });
  g.apply({ kind: "txn-end", partition: "p1", txn: "T3", version: 22 });
  assert.equal(g.suggestVictim(report), null);
});

test("stale snapshot never triggers a false kill", () => {
  const g = new WaitGraph();
  begins(g, "p1", ["T1", "T2"]);
  feed(g, [
    { kind: "edge-upsert", partition: "p1", from: "T1", to: "T2", version: 5 },
    { kind: "edge-upsert", partition: "p2", from: "T2", to: "T1", version: 5 },
  ]);
  const report = g.detect();
  assert.ok(report);

  // T1's lock is released before the abort decision arrives.
  g.apply({ kind: "edge-remove", partition: "p1", from: "T1", to: "T2", version: 6 });

  const result = g.abort(report, "T2");
  assert.equal(result.status, "stale-snapshot");
  assert.equal(g.isAlive("T2"), true, "stale report must not kill T2");
  assert.equal(g.detect(), null, "cycle is gone after the edge removal");
});

test("edge version bump also invalidates the snapshot", () => {
  const g = new WaitGraph();
  begins(g, "p1", ["T1", "T2"]);
  feed(g, [
    { kind: "edge-upsert", partition: "p1", from: "T1", to: "T2", version: 5 },
    { kind: "edge-upsert", partition: "p2", from: "T2", to: "T1", version: 5 },
  ]);
  const report = g.detect();
  assert.ok(report);

  // Same edge re-reported with a newer version: snapshot is stale.
  g.apply({ kind: "edge-upsert", partition: "p1", from: "T1", to: "T2", version: 6 });
  assert.equal(g.abort(report, "T1").status, "stale-snapshot");
  assert.equal(g.isAlive("T1"), true);
});

test("fresh snapshot aborts the victim exactly once", () => {
  const g = new WaitGraph();
  begins(g, "p1", ["T1", "T2"]);
  feed(g, [
    { kind: "edge-upsert", partition: "p1", from: "T1", to: "T2", version: 5 },
    { kind: "edge-upsert", partition: "p2", from: "T2", to: "T1", version: 5 },
  ]);
  const report = g.detect();
  assert.ok(report);

  const victim = g.suggestVictim(report);
  assert.equal(victim, "T1");
  const result = g.abort(report, victim as string);
  assert.deepEqual(result, { status: "aborted", victim: "T1" });
  assert.equal(g.isAlive("T1"), false);
  assert.equal(g.isAlive("T2"), true);

  // A second abort of the same victim is refused: it no longer exists.
  assert.equal(g.abort(report, "T1").status, "victim-not-live");
});

test("abort refuses a victim outside the cycle", () => {
  const g = new WaitGraph();
  begins(g, "p1", ["T1", "T2", "T9"]);
  feed(g, [
    { kind: "edge-upsert", partition: "p1", from: "T1", to: "T2", version: 1 },
    { kind: "edge-upsert", partition: "p2", from: "T2", to: "T1", version: 1 },
  ]);
  const report = g.detect();
  assert.ok(report);
  assert.equal(g.abort(report, "T9").status, "victim-not-in-cycle");
  assert.equal(g.isAlive("T9"), true);
});

test("stale txn-begin cannot resurrect an aborted victim", () => {
  const g = new WaitGraph();
  begins(g, "p1", ["T1", "T2"], 1); // T1@v1, T2@v2
  feed(g, [
    { kind: "edge-upsert", partition: "p1", from: "T1", to: "T2", version: 5 },
    { kind: "edge-upsert", partition: "p2", from: "T2", to: "T1", version: 5 },
  ]);
  const report = g.detect();
  assert.ok(report);
  assert.equal(g.abort(report, "T1").status, "aborted");

  // A delayed duplicate of the original begin must not revive T1.
  const r = g.apply({ kind: "txn-begin", partition: "p1", txn: "T1", version: 1 });
  assert.deepEqual(r, { applied: false, reason: "stale-version" });
  assert.equal(g.isAlive("T1"), false);
});

test("detector stats expose dropped out-of-order events", () => {
  const g = new WaitGraph();
  g.apply({ kind: "edge-upsert", partition: "p1", from: "T1", to: "T2", version: 3 });
  g.apply({ kind: "edge-upsert", partition: "p1", from: "T1", to: "T2", version: 1 });
  g.apply({ kind: "edge-remove", partition: "p1", from: "T1", to: "T2", version: 2 });
  const s = g.stats();
  assert.equal(s.liveEdges, 1);
  assert.equal(s.droppedEdgeEvents, 2);
});

test("self-loops are ignored", () => {
  const g = new WaitGraph();
  const r = g.apply({ kind: "edge-upsert", partition: "p1", from: "T1", to: "T1", version: 1 });
  assert.deepEqual(r, { applied: false, reason: "self-loop-ignored" });
  assert.equal(g.detect(), null);
});

test("edge identity is per-partition: same endpoints on two partitions both count", () => {
  const g = new WaitGraph();
  feed(g, [
    { kind: "edge-upsert", partition: "p1", from: "T1", to: "T2", version: 1 },
    { kind: "edge-upsert", partition: "p2", from: "T1", to: "T2", version: 1 },
  ]);
  assert.equal(g.liveEdges().length, 2);
  // Removing one copy leaves the other.
  g.apply({ kind: "edge-remove", partition: "p1", from: "T1", to: "T2", version: 2 });
  const remaining = g.liveEdges();
  assert.equal(remaining.length, 1);
  assert.equal(remaining[0].partition, "p2");
});

test("helper findShortestCycleInEdges returns null on acyclic input", () => {
  const edges: CycleEdge[] = [
    { partition: "p1", from: "A", to: "B", version: 1 },
    { partition: "p1", from: "B", to: "C", version: 1 },
    { partition: "p1", from: "A", to: "C", version: 1 },
  ];
  assert.equal(findShortestCycleInEdges(edges), null);
});
