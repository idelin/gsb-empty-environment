import { test } from "node:test";
import assert from "node:assert/strict";
import {
  complementRanges,
  intersectRanges,
  isEmpty,
  isFullLine,
  normalizeRanges,
  unionRanges,
} from "../src/ranges.ts";

test("normalize merges overlapping and adjacent integer boxes", () => {
  assert.deepEqual(
    normalizeRanges([
      { lo: 1, hi: 2 },
      { lo: 3, hi: 4 },
      { lo: 4, hi: 8 },
    ]),
    [{ lo: 1, hi: 8 }],
  );
});

test("intersection of boxes", () => {
  assert.deepEqual(intersectRanges([{ lo: 0, hi: 10 }], [{ lo: 5, hi: 20 }]), [{ lo: 5, hi: 10 }]);
  assert.equal(isEmpty(intersectRanges([{ lo: 0, hi: 4 }], [{ lo: 10, hi: 20 }])), true);
});

test("union and complement round-trip on the integer line", () => {
  const boxes = [{ lo: 3, hi: 7 }, { lo: -Infinity, hi: -2 }];
  assert.ok(isFullLine(unionRanges(boxes, complementRanges(boxes))));
  const half = [{ lo: 18, hi: Infinity }];
  assert.deepEqual(complementRanges(half), [{ lo: -Infinity, hi: 17 }]);
  assert.deepEqual(complementRanges(complementRanges(boxes)), normalizeRanges(boxes));
});

test("!= 2 is represented as two boxes", () => {
  assert.deepEqual(complementRanges([{ lo: 2, hi: 2 }]), [
    { lo: -Infinity, hi: 1 },
    { lo: 3, hi: Infinity },
  ]);
});
