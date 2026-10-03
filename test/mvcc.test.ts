import { test } from "node:test";
import assert from "node:assert/strict";
import { createStore, MvccReader, MvccWriter, commitBatch } from "../src/mvcc.ts";

function makeStore(keyCount = 8, poolSize = 256, base = 1000) {
  const store = createStore(keyCount, poolSize);
  store.baseVal.fill(base);
  return store;
}

test("reader sees base values on an empty store", () => {
  const store = makeStore();
  const reader = new MvccReader(store);
  const snap = reader.snapshot();
  assert.equal(reader.get(snap, 0), 1000);
  assert.equal(reader.get(snap, 7), 1000);
  assert.equal(reader.checksum(snap), 8000);
});

test("committed writes are visible to new snapshots", () => {
  const store = makeStore();
  const writer = new MvccWriter(store);
  const reader = new MvccReader(store);
  commitBatch(store, writer, [[3, 42]]);
  const snap = reader.snapshot();
  assert.equal(reader.get(snap, 3), 42);
  assert.equal(reader.get(snap, 4), 1000);
});

test("snapshot isolation: a pinned snapshot keeps seeing old versions", () => {
  const store = makeStore();
  const writer = new MvccWriter(store);
  const reader = new MvccReader(store);

  const snap1 = reader.snapshot();
  commitBatch(store, writer, [[0, 10]]);
  commitBatch(store, writer, [[0, 20]]);
  commitBatch(store, writer, [[0, 30]]);
  const snap2 = reader.snapshot();

  assert.equal(reader.get(snap1, 0), 1000, "old snapshot must not see later commits");
  assert.equal(reader.get(snap2, 0), 30, "new snapshot sees the latest committed version");
});

test("uncommitted (staged) writes are invisible to readers", () => {
  const store = makeStore();
  const writer = new MvccWriter(store);
  const reader = new MvccReader(store);

  writer.begin();
  writer.put(1, 1); // node already published to the chain head
  const snapMidTxn = reader.snapshot();
  assert.equal(reader.get(snapMidTxn, 1), 1000, "staged node must be skipped by txId");
  writer.commit();

  const snapAfter = reader.snapshot();
  assert.equal(reader.get(snapAfter, 1), 1);
});

test("multi-key batch commits atomically: mid-transaction readers see neither key", () => {
  const store = makeStore(4, 64, 500);
  const writer = new MvccWriter(store);
  const reader = new MvccReader(store);

  writer.begin();
  writer.put(0, 400); // -100
  writer.put(1, 600); // +100
  // Writer is between publish and commit: every possible snapshot here must
  // still observe the conserved sum.
  const sumMid = reader.checksum(reader.snapshot());
  assert.equal(sumMid, 2000);
  writer.commit();

  const sumAfter = reader.checksum(reader.snapshot());
  assert.equal(sumAfter, 2000);
  assert.equal(reader.get(reader.snapshot(), 0), 400);
  assert.equal(reader.get(reader.snapshot(), 1), 600);
});

test("version chain traversal picks the newest version visible to the snapshot", () => {
  const store = makeStore();
  const writer = new MvccWriter(store);
  const reader = new MvccReader(store);

  commitBatch(store, writer, [[2, 101]]); // tx 1
  const snapA = reader.snapshot();
  commitBatch(store, writer, [[2, 202]]); // tx 2
  const snapB = reader.snapshot();
  commitBatch(store, writer, [[2, 303]]); // tx 3

  assert.equal(reader.get(snapA, 2), 101);
  assert.equal(reader.get(snapB, 2), 202);
  assert.equal(reader.get(reader.snapshot(), 2), 303);
});

test("checksum stays conserved across many sequential transfer transactions", () => {
  const store = makeStore(100, 4096, 1000);
  const writer = new MvccWriter(store);
  const reader = new MvccReader(store);
  let x = 123456789;
  const rand = () => ((x = (1103515245 * x + 12345) & 0x7fffffff) / 0x7fffffff);

  const snapshots: number[] = [];
  for (let i = 0; i < 1000; i++) {
    const from = Math.floor(rand() * 100);
    let to = Math.floor(rand() * 100);
    if (to === from) to = (to + 1) % 100;
    const amount = 1 + Math.floor(rand() * 50);
    const vf = reader.get(reader.snapshot(), from);
    const vt = reader.get(reader.snapshot(), to);
    commitBatch(store, writer, [
      [from, vf - amount],
      [to, vt + amount],
    ]);
    snapshots.push(reader.snapshot());
  }
  for (const snap of snapshots) {
    assert.equal(reader.checksum(snap), 100_000);
  }
});
