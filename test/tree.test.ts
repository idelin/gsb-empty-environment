import { test } from "node:test";
import assert from "node:assert/strict";
import { AcyclicTreeCrdt } from "../src/tree.ts";
import { ROOT_ID } from "../src/types.ts";

/** Build two synced replicas that both know /dirA and /dirB. */
function syncedPair(): {
  node1: AcyclicTreeCrdt;
  node2: AcyclicTreeCrdt;
  dirA: string;
  dirB: string;
} {
  const node1 = new AcyclicTreeCrdt("node1");
  const node2 = new AcyclicTreeCrdt("node2");
  const dirA = node1.create("dirA");
  const dirB = node1.create("dirB");
  node2.merge(node1);
  node1.merge(node2);
  return { node1, node2, dirA, dirB };
}

test("local create / move / rename basics", () => {
  const r = new AcyclicTreeCrdt("r1");
  const a = r.create("dirA");
  const b = r.create("dirB");
  r.move(a, b);
  r.rename(a, "dirA2");
  assert.equal(r.pathOf(a), "/dirB/dirA2");
  assert.equal(r.pathOf(b), "/dirB");
  assert.ok(r.isAncestor(b, a));
  assert.ok(r.allReachable());
});

test("acceptance: concurrent swap moves — later op in total order is reverted", () => {
  const { node1, node2, dirA, dirB } = syncedPair();

  // Concurrent: node1 moves /dirA under /dirB, node2 moves /dirB under /dirA.
  node1.move(dirA, dirB);
  node2.move(dirB, dirA);

  // Merge in both directions.
  node1.merge(node2);
  node2.merge(node1);

  // Both replicas converge to the identical acyclic tree.
  assert.deepEqual(node1.state(), node2.state());

  // Equal Lamport clocks -> replica id breaks the tie: "node1" < "node2",
  // so node2's move is the LATER op in the deterministic total order and is
  // the one that gets reverted. dirA ends up under dirB, dirB stays at root.
  assert.equal(node1.pathOf(dirA), "/dirB/dirA");
  assert.equal(node1.pathOf(dirB), "/dirB");

  // The losing op is recorded with a cycle reason, and nothing is orphaned.
  const dropped = node1.droppedOps();
  assert.equal(dropped.length, 1);
  assert.equal(dropped[0].op.kind, "move");
  assert.equal((dropped[0].op as { ts: { replica: string } }).ts.replica, "node2");
  assert.match(dropped[0].reason, /cycle/);
  assert.ok(node1.allReachable());
  assert.ok(node2.allReachable());
});

test("deterministic total order: whichever op is later loses, regardless of replica", () => {
  const { node1, node2, dirA, dirB } = syncedPair();

  // Give node1 a strictly higher Lamport clock so ITS move is the later one.
  node1.rename(dirA, "dirA"); // bump clock only
  node2.move(dirB, dirA); // earlier in total order
  node1.move(dirA, dirB); // later in total order -> must be reverted

  node1.merge(node2);
  node2.merge(node1);

  assert.deepEqual(node1.state(), node2.state());
  // node2's earlier move wins: dirB sits under dirA, dirA stays at root.
  assert.equal(node1.pathOf(dirB), "/dirA/dirB");
  assert.equal(node1.pathOf(dirA), "/dirA");
  assert.ok(node1.allReachable());
});

test("three-way concurrent ring is broken, no directory lost", () => {
  const a = new AcyclicTreeCrdt("a");
  const b = new AcyclicTreeCrdt("b");
  const c = new AcyclicTreeCrdt("c");
  const d1 = a.create("d1");
  const d2 = a.create("d2");
  const d3 = a.create("d3");
  b.merge(a);
  c.merge(a);
  a.merge(b);
  a.merge(c);

  // Concurrent ring: d1->d2, d2->d3, d3->d1.
  a.move(d1, d2);
  b.move(d2, d3);
  c.move(d3, d1);

  a.merge(b);
  a.merge(c);
  b.merge(a);
  c.merge(a);

  assert.deepEqual(a.state(), b.state());
  assert.deepEqual(b.state(), c.state());
  assert.ok(a.allReachable());
  assert.equal(a.droppedOps().length, 1);
  // All three directories still exist and are reachable from root.
  for (const d of [d1, d2, d3]) assert.ok(a.pathOf(d).startsWith("/"));
});

test("concurrent renames resolve last-writer-wins deterministically", () => {
  const { node1, node2, dirA } = syncedPair();
  node1.rename(dirA, "alpha");
  node2.rename(dirA, "beta");
  node1.merge(node2);
  node2.merge(node1);
  // Same Lamport -> "node2" sorts later -> "beta" wins on both replicas.
  assert.equal(node1.pathOf(dirA), "/beta");
  assert.equal(node2.pathOf(dirA), "/beta");
});

test("merge is idempotent and commutative", () => {
  const { node1, node2, dirA, dirB } = syncedPair();
  node1.move(dirA, dirB);
  node2.move(dirB, dirA);

  const a = new AcyclicTreeCrdt("merge-a");
  a.merge(node1);
  a.merge(node2);
  a.merge(node1); // duplicate merge: no-op

  const b = new AcyclicTreeCrdt("merge-b");
  b.merge(node2);
  b.merge(node1);

  assert.deepEqual(a.state(), b.state());
  assert.ok(a.allReachable());
});

test("moving a node under itself or its descendant is rejected locally too", () => {
  const r = new AcyclicTreeCrdt("r1");
  const a = r.create("a");
  const b = r.create("b", a);
  r.move(a, a); // self
  r.move(a, b); // own descendant
  assert.equal(r.pathOf(a), "/a");
  assert.equal(r.pathOf(b), "/a/b");
  assert.equal(r.droppedOps().length, 2);
  assert.ok(r.allReachable());
});

test("root is immutable and always present", () => {
  const r = new AcyclicTreeCrdt("r1");
  const a = r.create("a");
  r.move(ROOT_ID, a); // nonsense op gets dropped: root is never registered as movable
  assert.equal(r.pathOf(ROOT_ID), "/");
  assert.ok(r.allReachable());
});
