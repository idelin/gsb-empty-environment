import assert from "node:assert/strict";
import test from "node:test";
import {
  intersectIntervals,
  intervalContains,
  isEmptyInterval,
  validBetween,
} from "../src/index.ts";

test("interval endpoints honor inclusive and exclusive flags", () => {
  const halfOpen = { start: 10, end: 20, startInclusive: true, endInclusive: false };
  assert.equal(intervalContains(halfOpen, 10), true);
  assert.equal(intervalContains(halfOpen, 20), false);

  const openClosed = { start: 10, end: 20, startInclusive: false, endInclusive: true };
  const overlap = intersectIntervals(halfOpen, openClosed);
  assert.deepEqual(overlap, {
    start: 10,
    end: 20,
    startInclusive: false,
    endInclusive: false,
  });
  assert.equal(intervalContains(overlap, 10), false);
  assert.equal(intervalContains(overlap, 20), false);

  assert.equal(isEmptyInterval({ start: 20, end: 20, endInclusive: false }), true);
  assert.equal(isEmptyInterval({ start: 20, end: 20, endInclusive: true }), false);
  assert.equal(
    isEmptyInterval({ start: 20, end: 20, startInclusive: true, endInclusive: true }),
    false,
  );
  assert.equal(intersectIntervals(halfOpen, { start: 20, end: 30 }), undefined);
  assert.deepEqual(validBetween(10, 20), {
    start: 10,
    end: 20,
    startInclusive: true,
    endInclusive: false,
  });
});
