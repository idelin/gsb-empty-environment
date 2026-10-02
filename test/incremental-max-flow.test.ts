import { test } from "node:test";
import assert from "node:assert/strict";
import {
  IncrementalMaxFlow,
  type MinCutCertificate,
} from "../src/incremental-max-flow.ts";

// Acceptance network (node ids): s=0, a=1, b=2, d=3, t=4
//
//   s -> a (5)   s -> b (5)   a -> b (5, key edge)
//   a -> d (1)   d -> t (1)   b -> t (10)
//
// Initial max flow = 10:  s->a->b->t (5), s->b->t (5).
// Key edge a->b carries 5. Reducing it 5 -> 2 forces a 3-unit reverse
// withdrawal; the alternative path s->a->d->t recovers 1 unit => flow 8.

const S = 0, A = 1, B = 2, D = 3, T = 4;

function buildNetwork() {
  const mf = new IncrementalMaxFlow(5, S, T);
  const eSA = mf.addEdge(S, A, 5);
  const eSB = mf.addEdge(S, B, 5);
  const eAB = mf.addEdge(A, B, 5); // key edge
  const eAD = mf.addEdge(A, D, 1);
  const eDT = mf.addEdge(D, T, 1);
  const eBT = mf.addEdge(B, T, 10);
  return { mf, eSA, eSB, eAB, eAD, eDT, eBT };
}

function assertValidCertificate(
  mf: IncrementalMaxFlow,
  cert: MinCutCertificate,
  expectedValue: number,
) {
  assert.equal(cert.maxFlow, expectedValue);
  // max-flow min-cut theorem: cut capacity equals flow value
  assert.equal(cert.cutCapacity, expectedValue);
  assert.equal(
    cert.cutEdges.reduce((sum, e) => sum + e.capacity, 0),
    cert.cutCapacity,
  );
  // partition covers every node exactly once; source/sink on correct sides
  assert.deepEqual(
    [...cert.sourceSide, ...cert.sinkSide].sort((x, y) => x - y),
    [0, 1, 2, 3, 4],
  );
  assert.ok(cert.sourceSide.includes(S));
  assert.ok(cert.sinkSide.includes(T));
  // every cut edge goes S -> T and is saturated
  const inS = new Set(cert.sourceSide);
  for (const e of cert.cutEdges) {
    assert.ok(inS.has(e.from) && !inS.has(e.to));
    assert.equal(e.flow, e.capacity);
  }
  // flow conservation + capacity constraints
  const balance = new Map<number, number>();
  for (const id of [0, 1, 2, 3, 4, 5]) {
    const e = mf.getEdge(id);
    assert.ok(e.flow <= e.capacity && e.flow >= 0);
    balance.set(e.from, (balance.get(e.from) ?? 0) - e.flow);
    balance.set(e.to, (balance.get(e.to) ?? 0) + e.flow);
  }
  for (const [node, b] of balance) {
    if (node === S) assert.equal(b, -expectedValue);
    else if (node === T) assert.equal(b, expectedValue);
    else assert.equal(b, 0, `conservation violated at node ${node}`);
  }
}

test("initial max flow is 10 with a valid min-cut certificate", () => {
  const { mf } = buildNetwork();
  const cert = mf.computeMaxFlow();
  assertValidCertificate(mf, cert, 10);
  // key edge a->b is saturated carrying 5 units
  assert.equal(mf.getEdge(2).flow, 5);
});

test("reducing key edge 5 -> 2 withdraws 3 units and recovers 1 via alternative path", () => {
  const { mf, eAB } = buildNetwork();
  mf.computeMaxFlow();

  const cert = mf.decreaseCapacity(eAB, 3); // capacity 5 -> 2

  assertValidCertificate(mf, cert, 8);
  // key edge clamped to its new capacity
  assert.deepEqual(mf.getEdge(eAB), { id: eAB, from: A, to: B, capacity: 2, flow: 2 });
  // withdrawal + re-routing leaves exactly these flows
  assert.equal(mf.getEdge(0).flow, 3); // s->a: 2 via key edge + 1 via alternative
  assert.equal(mf.getEdge(3).flow, 1); // a->d alternative now saturated
  assert.equal(mf.getEdge(4).flow, 1); // d->t
  assert.equal(mf.getEdge(5).flow, 7); // b->t: 5 direct + 2 via key edge
  // reachability partition: only {s, a} remain reachable from s
  assert.deepEqual(cert.sourceSide, [S, A]);
  assert.deepEqual(cert.sinkSide, [B, D, T]);
  // cut edges: s->b (5), a->b (2), a->d (1)  =>  8
  assert.deepEqual(
    cert.cutEdges.map((e) => [e.id, e.capacity]),
    [
      [1, 5],
      [2, 2],
      [3, 1],
    ],
  );
});

test("capacity decrease below current flow value is a no-op for the flow", () => {
  const { mf, eAB } = buildNetwork();
  mf.computeMaxFlow();
  mf.decreaseCapacity(eAB, 3); // flow now 8, edge b->t carries 7 < 10

  const cert = mf.decreaseCapacity(5, 1); // b->t capacity 10 -> 9 (flow 7 <= 9)

  assertValidCertificate(mf, cert, 8);
  assert.equal(mf.getEdge(5).capacity, 9);
});

test("increasing capacity restores flow along the freed edge", () => {
  const { mf, eAB } = buildNetwork();
  mf.computeMaxFlow();
  mf.decreaseCapacity(eAB, 3); // -> 8

  const cert = mf.increaseCapacity(eAB, 3); // back to 5

  assertValidCertificate(mf, cert, 10);
});

test("batched capacity changes emit one certificate per change", () => {
  const { mf, eAB } = buildNetwork();
  const initial = mf.computeMaxFlow();
  assert.equal(initial.maxFlow, 10);

  const certs = mf.applyBatch([
    { edgeId: eAB, newCapacity: 2 }, // shrink -> 8
    { edgeId: eAB, newCapacity: 0 }, // cut the key edge entirely -> 6
    { edgeId: eAB, newCapacity: 5 }, // restore -> back up
  ]);

  assert.equal(certs.length, 3);
  assert.equal(certs[0].maxFlow, 8);
  assert.equal(certs[0].cutCapacity, 8);
  assert.equal(certs[1].maxFlow, 6); // s->a->d->t (1) + s->b->t (5)
  assertValidCertificate(mf, certs[2], 10);
  assert.equal(mf.value, 10);
});

test("reducing the key edge to 0 drops the flow to 6", () => {
  const { mf, eAB } = buildNetwork();
  mf.computeMaxFlow();
  const cert = mf.setCapacity(eAB, 0);
  assertValidCertificate(mf, cert, 6);
  assert.deepEqual(cert.sourceSide, [S, A]);
});

test("invalid inputs are rejected", () => {
  const { mf, eAB } = buildNetwork();
  mf.computeMaxFlow();
  assert.throws(() => mf.decreaseCapacity(eAB, 6), RangeError); // below zero
  assert.throws(() => mf.increaseCapacity(99, 1), RangeError); // unknown edge
  assert.throws(() => mf.increaseCapacity(eAB, 0), RangeError); // non-positive delta
  assert.throws(() => mf.setCapacity(eAB, 1.5), RangeError); // non-integer
  assert.throws(() => mf.addEdge(0, 1, -1), RangeError); // negative capacity
});
