import assert from "node:assert/strict";
import test from "node:test";
import {
  cmpToIntervals,
  complement,
  contains,
  coversDomain,
  intersect,
  normalize,
  setToIntervals,
  union,
} from "../src/interval.ts";

test("normalize merges overlapping and integer-adjacent intervals", () => {
  assert.deepEqual(
    normalize([
      { lo: 5, hi: 8 },
      { lo: 1, hi: 3 },
      { lo: 4, hi: 4 }, // adjacent to [1,3] and [5,8]
      { lo: 20, hi: 10 }, // empty, dropped
    ]),
    [{ lo: 1, hi: 8 }],
  );
});

test("intersect computes interval conjunction", () => {
  assert.deepEqual(
    intersect(
      [{ lo: 18, hi: Infinity }],
      [{ lo: -Infinity, hi: 65 }],
    ),
    [{ lo: 18, hi: 65 }],
  );
  assert.deepEqual(
    intersect([{ lo: 1, hi: 5 }], [{ lo: 10, hi: 20 }]),
    [],
  );
});

test("union merges interval disjunction", () => {
  assert.deepEqual(
    union([{ lo: 1, hi: 2 }], [{ lo: 3, hi: 4 }]),
    [{ lo: 1, hi: 4 }],
  );
});

test("complement within the integers", () => {
  assert.deepEqual(complement([{ lo: 3, hi: Infinity }]), [
    { lo: -Infinity, hi: 2 },
  ]);
  assert.deepEqual(complement([{ lo: -Infinity, hi: Infinity }]), []);
  assert.deepEqual(complement([]), [{ lo: -Infinity, hi: Infinity }]);
});

test("cmpToIntervals covers all six operators", () => {
  assert.deepEqual(cmpToIntervals("<", 5), [{ lo: -Infinity, hi: 4 }]);
  assert.deepEqual(cmpToIntervals("<=", 5), [{ lo: -Infinity, hi: 5 }]);
  assert.deepEqual(cmpToIntervals(">", 5), [{ lo: 6, hi: Infinity }]);
  assert.deepEqual(cmpToIntervals(">=", 5), [{ lo: 5, hi: Infinity }]);
  assert.deepEqual(cmpToIntervals("==", 5), [{ lo: 5, hi: 5 }]);
  assert.deepEqual(cmpToIntervals("!=", 5), [
    { lo: -Infinity, hi: 4 },
    { lo: 6, hi: Infinity },
  ]);
});

test("setToIntervals is exact for finite integer sets", () => {
  assert.deepEqual(setToIntervals([3, 1, 2, 10]), [
    { lo: 1, hi: 3 },
    { lo: 10, hi: 10 },
  ]);
});

test("contains and coversDomain", () => {
  const ivs = normalize([
    { lo: 1, hi: 3 },
    { lo: 10, hi: 10 },
  ]);
  assert.equal(contains(ivs, 2), true);
  assert.equal(contains(ivs, 5), false);
  assert.equal(coversDomain([{ lo: -Infinity, hi: Infinity }], 0, 120), true);
  assert.equal(coversDomain([{ lo: 0, hi: 119 }], 0, 120), false);
});
