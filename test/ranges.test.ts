import { test } from "node:test";
import assert from "node:assert/strict";
import {
  addIndex,
  countIndex,
  expandRanges,
  normalizeRanges,
  rangeCount,
  removeIndex,
  type Range,
} from "../src/ranges.ts";

test("1..100 consecutive indices compress to a single range", () => {
  let ranges: Range[] = [];
  for (let i = 1; i <= 100; i++) ranges = addIndex(ranges, i);
  assert.deepEqual(ranges, [[1, 100, 1]]);
  assert.equal(rangeCount(ranges), 100);
});

test("a missing middle index must NOT be written as continuous", () => {
  let ranges: Range[] = [];
  for (let i = 1; i <= 100; i++) {
    if (i === 50) continue;
    ranges = addIndex(ranges, i);
  }
  assert.deepEqual(ranges, [
    [1, 49, 1],
    [51, 100, 1],
  ]);
  assert.equal(rangeCount(ranges), 99);
  assert.equal(countIndex(ranges, 50), 0);
  assert.equal(countIndex(ranges, 49), 1);
});

test("insertion order does not matter", () => {
  let ranges: Range[] = [];
  for (const i of [5, 1, 3, 2, 4, 9, 7, 8]) ranges = addIndex(ranges, i);
  assert.deepEqual(ranges, [
    [1, 5, 1],
    [7, 9, 1],
  ]);
});

test("duplicate indices are tracked via multiplicity", () => {
  let ranges: Range[] = [];
  ranges = addIndex(ranges, 3);
  ranges = addIndex(ranges, 3);
  ranges = addIndex(ranges, 3);
  assert.deepEqual(ranges, [[3, 3, 3]]);
  assert.deepEqual(expandRanges(ranges), [3, 3, 3]);
});

test("duplicates do not break adjacency of neighbours", () => {
  let ranges: Range[] = [];
  for (const i of [1, 2, 2, 3]) ranges = addIndex(ranges, i);
  assert.deepEqual(ranges, [
    [1, 1, 1],
    [2, 2, 2],
    [3, 3, 1],
  ]);
  assert.deepEqual(expandRanges(ranges), [1, 2, 2, 3]);
});

test("removing the middle of a range splits it", () => {
  const removed = removeIndex([[1, 100, 1]], 50);
  assert.deepEqual(removed, [
    [1, 49, 1],
    [51, 100, 1],
  ]);
});

test("removing an absent index returns null", () => {
  assert.equal(removeIndex([[1, 10, 1]], 42), null);
  assert.equal(removeIndex([], 1), null);
});

test("removing one occurrence only decrements multiplicity", () => {
  const removed = removeIndex([[5, 5, 3]], 5);
  assert.deepEqual(removed, [[5, 5, 2]]);
});

test("add/remove round trip restores the original ranges", () => {
  let ranges: Range[] = [];
  for (let i = 0; i < 50; i++) ranges = addIndex(ranges, i);
  const snapshot = ranges.map((r) => [...r]);
  for (const i of [10, 20, 30]) ranges = addIndex(ranges, i);
  for (const i of [10, 20, 30]) {
    const next = removeIndex(ranges, i);
    assert.ok(next !== null);
    ranges = next;
  }
  assert.deepEqual(ranges, snapshot);
});

test("normalizeRanges merges adjacent equal-mult ranges and drops empties", () => {
  assert.deepEqual(
    normalizeRanges([
      [5, 5, 0],
      [3, 4, 1],
      [1, 2, 1],
      [8, 6, 1],
    ]),
    [[1, 4, 1]],
  );
});
