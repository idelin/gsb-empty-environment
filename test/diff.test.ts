import { test } from "node:test";
import assert from "node:assert/strict";
import { diffLines } from "../src/diff.ts";

test("identical inputs yield a single equal block", () => {
  const blocks = diffLines(["a", "b"], ["a", "b"]);
  assert.deepEqual(blocks, [{ kind: "equal", aStart: 0, aEnd: 2, bStart: 0, bEnd: 2 }]);
});

test("empty inputs yield no blocks", () => {
  assert.deepEqual(diffLines([], []), []);
});

test("insertion in the middle", () => {
  const blocks = diffLines(["a", "b", "c"], ["a", "x", "b", "c"]);
  assert.deepEqual(blocks, [
    { kind: "equal", aStart: 0, aEnd: 1, bStart: 0, bEnd: 1 },
    { kind: "change", aStart: 1, aEnd: 1, bStart: 1, bEnd: 2 },
    { kind: "equal", aStart: 1, aEnd: 3, bStart: 2, bEnd: 4 },
  ]);
});

test("deletion in the middle", () => {
  const blocks = diffLines(["a", "x", "b"], ["a", "b"]);
  assert.deepEqual(blocks, [
    { kind: "equal", aStart: 0, aEnd: 1, bStart: 0, bEnd: 1 },
    { kind: "change", aStart: 1, aEnd: 2, bStart: 1, bEnd: 1 },
    { kind: "equal", aStart: 2, aEnd: 3, bStart: 1, bEnd: 2 },
  ]);
});

test("replacement merges delete and insert into one change block", () => {
  const blocks = diffLines(["a", "b"], ["a", "c"]);
  assert.deepEqual(blocks, [
    { kind: "equal", aStart: 0, aEnd: 1, bStart: 0, bEnd: 1 },
    { kind: "change", aStart: 1, aEnd: 2, bStart: 1, bEnd: 2 },
  ]);
});

test("deterministic tie-break: earliest repeated occurrence is kept", () => {
  // Two identical lines on side A, one on side B: the FIRST is aligned,
  // the second is reported as deleted. This must never vary.
  const blocks = diffLines(["x", "x"], ["x"]);
  assert.deepEqual(blocks, [
    { kind: "equal", aStart: 0, aEnd: 1, bStart: 0, bEnd: 1 },
    { kind: "change", aStart: 1, aEnd: 2, bStart: 1, bEnd: 1 },
  ]);
});

test("repeated blank lines produce a stable diff", () => {
  const a = ["", "", "", ""];
  const b = ["", ""];
  const expected = [
    { kind: "equal", aStart: 0, aEnd: 2, bStart: 0, bEnd: 2 },
    { kind: "change", aStart: 2, aEnd: 4, bStart: 2, bEnd: 2 },
  ];
  assert.deepEqual(diffLines(a, b), expected);
  // Stability: identical result across repeated runs.
  for (let i = 0; i < 5; i++) {
    assert.deepEqual(diffLines(a, b), expected);
  }
});

test("blank lines interleaved with content stay stable", () => {
  const a = ["p", "", "", "q", "", "r"];
  const b = ["p", "", "q", "", "", "r"];
  const first = diffLines(a, b);
  const second = diffLines(a, b);
  assert.deepEqual(first, second);
  // Blocks partition both sides contiguously.
  let aPos = 0;
  let bPos = 0;
  for (const block of first) {
    assert.equal(block.aStart, aPos);
    assert.equal(block.bStart, bPos);
    aPos = block.aEnd;
    bPos = block.bEnd;
  }
  assert.equal(aPos, a.length);
  assert.equal(bPos, b.length);
});

test("swapped inputs produce mirrored blocks", () => {
  const a = ["1", "2", "3", "4"];
  const b = ["1", "x", "3"];
  const ab = diffLines(a, b);
  const ba = diffLines(b, a);
  assert.deepEqual(ab, [
    { kind: "equal", aStart: 0, aEnd: 1, bStart: 0, bEnd: 1 },
    { kind: "change", aStart: 1, aEnd: 2, bStart: 1, bEnd: 2 },
    { kind: "equal", aStart: 2, aEnd: 3, bStart: 2, bEnd: 3 },
    { kind: "change", aStart: 3, aEnd: 4, bStart: 3, bEnd: 3 },
  ]);
  // Mirrored: A/B ranges swapped, same block kinds in the same order.
  assert.deepEqual(
    ba,
    ab.map((blk) => ({
      kind: blk.kind,
      aStart: blk.bStart,
      aEnd: blk.bEnd,
      bStart: blk.aStart,
      bEnd: blk.aEnd,
    })),
  );
});
