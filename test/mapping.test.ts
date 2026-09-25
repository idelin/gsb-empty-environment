import { test } from "node:test";
import assert from "node:assert/strict";
import { diffLines } from "../src/diff.ts";
import { LineMapping, computeFolds } from "../src/mapping.ts";

test("deleted lines map to null, not to a neighbour", () => {
  const blocks = diffLines(["a", "x", "b"], ["a", "b"]);
  const map = new LineMapping(blocks, 3, 2);
  assert.equal(map.aToB(0), 0);
  assert.equal(map.aToB(1), null); // deleted line: no counterpart
  assert.equal(map.aToB(2), 1);
  assert.equal(map.bToA(0), 0);
  assert.equal(map.bToA(1), 2);
});

test("inserted lines map to null on the other side", () => {
  const blocks = diffLines(["a", "b"], ["a", "x", "b"]);
  const map = new LineMapping(blocks, 2, 3);
  assert.equal(map.bToA(0), 0);
  assert.equal(map.bToA(1), null); // inserted line: no counterpart
  assert.equal(map.bToA(2), 1);
  assert.equal(map.aToB(1), 2);
});

test("changed regions map to null in both directions", () => {
  const blocks = diffLines(["a", "b", "c"], ["a", "z", "c"]);
  const map = new LineMapping(blocks, 3, 3);
  assert.equal(map.aToB(1), null);
  assert.equal(map.bToA(1), null);
  assert.equal(map.aToB(2), 2);
});

test("identity mapping when texts are equal", () => {
  const lines = Array.from({ length: 50 }, (_, i) => `line-${i}`);
  const map = new LineMapping(diffLines(lines, lines), 50, 50);
  for (let i = 0; i < 50; i++) {
    assert.equal(map.aToB(i), i);
    assert.equal(map.bToA(i), i);
  }
});

test("out-of-range lines throw", () => {
  const map = new LineMapping(diffLines(["a"], ["a"]), 1, 1);
  assert.throws(() => map.aToB(1), /out of range/);
  assert.throws(() => map.bToA(-1), /out of range/);
});

test("long equal regions fold, keeping context around changes", () => {
  const middle = Array.from({ length: 100 }, (_, i) => `same-${i}`);
  const a = ["head", ...middle, "tail-a"];
  const b = ["head", ...middle, "tail-b"];
  const blocks = diffLines(a, b);
  const folds = computeFolds(blocks, 2);
  assert.equal(folds.length, 1);
  assert.deepEqual(folds[0], {
    aStart: 2,
    aEnd: 99,
    bStart: 2,
    bEnd: 99,
    hiddenLines: 97,
  });
});

test("mapping stays valid through folded regions", () => {
  const a = Array.from({ length: 200 }, (_, i) => `row-${i}`);
  const blocks = diffLines(a, a);
  const map = new LineMapping(blocks, 200, 200);
  const folds = computeFolds(blocks, 5);
  assert.equal(folds.length, 1);
  // A line hidden inside the fold still maps linearly.
  const hiddenLine = folds[0].aStart + 10;
  assert.equal(map.aToB(hiddenLine), hiddenLine);
});

test("no folds when there is nothing to collapse", () => {
  const blocks = diffLines(["a"], ["b"]);
  assert.deepEqual(computeFolds(blocks, 3), []);
  assert.deepEqual(computeFolds(diffLines([], []), 3), []);
});
