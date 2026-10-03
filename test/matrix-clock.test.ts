import { test } from "node:test";
import assert from "node:assert/strict";
import { MatrixClock } from "../src/matrix-clock.ts";

const cluster = ["A", "B", "C"];

test("tick advances the node's own vector-clock component", () => {
  const clock = new MatrixClock(cluster, "A");
  assert.equal(clock.tick(), 1);
  assert.equal(clock.tick(), 2);
  assert.equal(clock.valueAt("A", "A"), 2);
  assert.equal(clock.valueAt("A", "B"), 0);
});

test("acknowledge records locally confirmed LSN per origin", () => {
  const clock = new MatrixClock(cluster, "B");
  clock.acknowledge("A", 7);
  clock.acknowledge("A", 3);
  assert.equal(clock.valueAt("B", "A"), 7);
});

test("merge takes element-wise max across all rows and is idempotent", () => {
  const a = new MatrixClock(cluster, "A");
  const b = new MatrixClock(cluster, "B");
  a.tick();
  a.tick();
  b.acknowledge("A", 2);
  b.acknowledge("C", 5);

  a.merge(b.snapshot());
  assert.equal(a.valueAt("B", "A"), 2);
  assert.equal(a.valueAt("B", "C"), 5);
  assert.equal(a.valueAt("A", "A"), 2);

  const before = a.snapshot();
  a.merge(b.snapshot());
  assert.deepEqual(a.snapshot(), before);
});

test("snapshot is a deep copy", () => {
  const clock = new MatrixClock(cluster, "A");
  clock.tick();
  const snap = clock.snapshot();
  snap[0][0] = 999;
  assert.equal(clock.valueAt("A", "A"), 1);
});

test("gcWatermark is the column-wise minimum across all rows", () => {
  const clock = new MatrixClock(cluster, "A");
  clock.acknowledge("A", 10);
  const remote = clock.snapshot();
  remote[1][0] = 8;
  remote[2][0] = 12;
  clock.merge(remote);
  assert.equal(clock.gcWatermark("A"), 8);
});

test("unknown node id is rejected", () => {
  assert.throws(() => new MatrixClock(cluster, "Z"), /not in cluster/);
  const clock = new MatrixClock(cluster, "A");
  assert.throws(() => clock.gcWatermark("Z"), /unknown node id/);
});
