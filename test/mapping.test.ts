import { test } from "node:test";
import assert from "node:assert/strict";
import { mapOffsetThroughEdit, mapRangeThroughEdit } from "../src/mapping.ts";
import { edit, range, type RangeAffinity } from "../src/types.ts";

const absorb: RangeAffinity = { start: "after", end: "after" };
const exclude: RangeAffinity = { start: "before", end: "before" };

test("insertion before the range shifts both bounds", () => {
  const result = mapRangeThroughEdit(range(4, 8), edit(1, 1, "abc"), absorb);
  assert.deepEqual(result, { valid: true, range: { start: 7, end: 11 } });
});

test("insertion after the range leaves it untouched", () => {
  const result = mapRangeThroughEdit(range(4, 8), edit(10, 10, "abc"), absorb);
  assert.deepEqual(result, { valid: true, range: { start: 4, end: 8 } });
});

test("insertion at the start boundary follows start affinity", () => {
  // "after": the boundary moves past the inserted text — it lands outside the range.
  const outside = mapRangeThroughEdit(range(4, 8), edit(4, 4, "xy"), absorb);
  assert.deepEqual(outside, { valid: true, range: { start: 6, end: 10 } });
  // "before": the boundary stays in front of the inserted text — the range absorbs it.
  const inside = mapRangeThroughEdit(range(4, 8), edit(4, 4, "xy"), exclude);
  assert.deepEqual(inside, { valid: true, range: { start: 4, end: 10 } });
});

test("insertion at the end boundary follows end affinity", () => {
  // "after" absorbs the typed text: the range grows — continued typing at the cursor.
  const grown = mapRangeThroughEdit(range(4, 8), edit(8, 8, "xy"), absorb);
  assert.deepEqual(grown, { valid: true, range: { start: 4, end: 10 } });
  // "before" keeps the inserted text outside the range.
  const fixed = mapRangeThroughEdit(range(4, 8), edit(8, 8, "xy"), exclude);
  assert.deepEqual(fixed, { valid: true, range: { start: 4, end: 8 } });
});

test("insertion strictly inside the range invalidates it", () => {
  assert.deepEqual(mapRangeThroughEdit(range(4, 8), edit(6, 6, "x"), absorb), { valid: false });
});

test("deletion before the range shifts it left", () => {
  const result = mapRangeThroughEdit(range(4, 8), edit(1, 3, ""), absorb);
  assert.deepEqual(result, { valid: true, range: { start: 2, end: 6 } });
});

test("deletion touching the interior invalidates the range", () => {
  // overlap from the left
  assert.deepEqual(mapRangeThroughEdit(range(4, 8), edit(2, 5, ""), absorb), { valid: false });
  // overlap from the right
  assert.deepEqual(mapRangeThroughEdit(range(4, 8), edit(7, 12, ""), absorb), { valid: false });
  // fully covered
  assert.deepEqual(mapRangeThroughEdit(range(4, 8), edit(0, 20, ""), absorb), { valid: false });
  // exact match
  assert.deepEqual(mapRangeThroughEdit(range(4, 8), edit(4, 8, ""), absorb), { valid: false });
  // single interior character
  assert.deepEqual(mapRangeThroughEdit(range(4, 8), edit(5, 6, ""), absorb), { valid: false });
});

test("replacement intersecting the range invalidates it", () => {
  assert.deepEqual(mapRangeThroughEdit(range(4, 8), edit(6, 7, "hello"), absorb), { valid: false });
});

test("deletion adjacent to a boundary stays valid", () => {
  // deletes the character immediately before the range
  const before = mapRangeThroughEdit(range(4, 8), edit(3, 4, ""), absorb);
  assert.deepEqual(before, { valid: true, range: { start: 3, end: 7 } });
  // deletes the character immediately after the range
  const after = mapRangeThroughEdit(range(4, 8), edit(8, 9, ""), absorb);
  assert.deepEqual(after, { valid: true, range: { start: 4, end: 8 } });
});

test("empty range: insertion at the point maps per affinity", () => {
  const after = mapRangeThroughEdit(range(5, 5), edit(5, 5, "ab"), absorb);
  assert.deepEqual(after, { valid: true, range: { start: 7, end: 7 } });
  const before = mapRangeThroughEdit(range(5, 5), edit(5, 5, "ab"), exclude);
  assert.deepEqual(before, { valid: true, range: { start: 5, end: 5 } });
});

test("empty range: deletion spanning the point invalidates it", () => {
  assert.deepEqual(mapRangeThroughEdit(range(5, 5), edit(4, 6, ""), absorb), { valid: false });
  // deletion ending exactly at the point is fine
  const ok = mapRangeThroughEdit(range(5, 5), edit(3, 5, ""), absorb);
  assert.deepEqual(ok, { valid: true, range: { start: 3, end: 3 } });
});

test("mapOffsetThroughEdit shifts offsets around an edit", () => {
  const e = edit(3, 6, "abcd"); // removes 3, inserts 4 -> delta +1
  assert.equal(mapOffsetThroughEdit(2, e, "after"), 2);
  assert.equal(mapOffsetThroughEdit(7, e, "after"), 8);
  assert.equal(mapOffsetThroughEdit(3, e, "before"), 3);
  assert.equal(mapOffsetThroughEdit(3, e, "after"), 7);
  assert.equal(mapOffsetThroughEdit(6, e, "before"), 3);
  assert.equal(mapOffsetThroughEdit(6, e, "after"), 7);
});
