import { test } from "node:test";
import assert from "node:assert/strict";
import { Node } from "../src/node.ts";

const cluster = ["A", "B", "C"];

function runRounds() {
  const A = new Node("A", cluster);
  const B = new Node("B", cluster);
  const C = new Node("C", cluster);
  for (let i = 0; i < 5; i++) A.localEvent(`tx-${i + 1}`);
  B.receive(A.send());
  C.receive(A.send());
  for (let i = 5; i < 10; i++) A.localEvent(`tx-${i + 1}`);
  B.receive(A.send());
  C.receive(A.send());
  B.localEvent("b-local-1");
  C.localEvent("c-local-1");
  C.receive(B.send());
  B.receive(C.send());
  return { A, B, C };
}

test("log replication: B and C hold A's events after round 2", () => {
  const { B, C } = runRounds();
  const bFromA = B.log.filter((e) => e.origin === "A");
  const cFromA = C.log.filter((e) => e.origin === "A");
  assert.equal(bFromA.length, 10);
  assert.equal(cFromA.length, 10);
  assert.deepEqual(bFromA.map((e) => e.lsn), [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]);
});

test("safety: no truncation before every node has acknowledged", () => {
  const { A } = runRounds();
  assert.equal(A.gcWatermark("A"), 0);
  const gc = A.collectGarbage();
  assert.equal(gc.removed.length, 0);
  assert.equal(A.log.filter((e) => e.origin === "A").length, 10);
});

test("A computes watermark=10 after B/C report back, then truncates physically", () => {
  const { A, B, C } = runRounds();
  A.receive(B.send());
  A.receive(C.send());

  assert.equal(A.gcWatermark("A"), 10);

  A.localEvent("tx-11");
  A.localEvent("tx-12");

  const gc = A.collectGarbage();
  assert.equal(gc.truncatedUpTo.get("A"), 10);
  const removedFromA = gc.removed.filter((e) => e.origin === "A");
  assert.equal(removedFromA.length, 10);
  assert.deepEqual(
    removedFromA.map((e) => e.lsn),
    [1, 2, 3, 4, 5, 6, 7, 8, 9, 10],
  );
  // B/C 的本地事件（各 LSN=1）同样已被全员知悉，一并回收
  assert.equal(gc.truncatedUpTo.get("B"), 1);
  assert.equal(gc.truncatedUpTo.get("C"), 1);

  const remaining = A.log.filter((e) => e.origin === "A").map((e) => e.lsn);
  assert.deepEqual(remaining, [11, 12]);
});

test("watermark is limited by the slowest node", () => {
  const A = new Node("A", cluster);
  const B = new Node("B", cluster);
  const C = new Node("C", cluster);
  for (let i = 0; i < 10; i++) A.localEvent(`tx-${i + 1}`);
  B.receive(A.send()); // 只有 B 收到 A 的事件
  A.receive(B.send()); // B 回执；C 从未确认
  assert.equal(A.gcWatermark("A"), 0);
  C.receive(A.send());
  A.receive(C.send());
  assert.equal(A.gcWatermark("A"), 10);
});

test("garbage collection is monotone and idempotent", () => {
  const { A, B, C } = runRounds();
  A.receive(B.send());
  A.receive(C.send());
  const first = A.collectGarbage();
  assert.equal(first.removed.filter((e) => e.origin === "A").length, 10);
  const second = A.collectGarbage();
  assert.equal(second.removed.length, 0);
  assert.equal(second.truncatedUpTo.get("A"), 10);
});

test("message merge never regresses clocks", () => {
  const { A, B } = runRounds();
  A.receive(B.send());
  const snap = A.clock.snapshot();
  B.receive(A.send());
  A.receive(B.send());
  const after = A.clock.snapshot();
  for (let r = 0; r < 3; r++) {
    for (let c = 0; c < 3; c++) {
      assert.ok(after[r][c] >= snap[r][c]);
    }
  }
});
