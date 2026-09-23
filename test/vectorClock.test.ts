import assert from "node:assert/strict";
import { test } from "node:test";
import {
  causallyReady,
  compare,
  mergeClocks,
  tick,
} from "../src/vectorClock.ts";

test("tick increments only the local counter", () => {
  assert.deepEqual(tick({ a: 1, b: 2 }, "a"), { a: 2, b: 2 });
  assert.deepEqual(tick({}, "a"), { a: 1 });
});

test("compare detects before/after/equal/concurrent", () => {
  assert.equal(compare({ a: 1 }, { a: 1, b: 1 }), "before");
  assert.equal(compare({ a: 1, b: 1 }, { a: 1 }), "after");
  assert.equal(compare({ a: 1 }, { a: 1 }), "equal");
  assert.equal(compare({ a: 1 }, { b: 1 }), "concurrent");
});

test("mergeClocks takes componentwise maximum", () => {
  assert.deepEqual(mergeClocks({ a: 2, b: 1 }, { a: 1, b: 3, c: 1 }), {
    a: 2,
    b: 3,
    c: 1,
  });
});

test("causallyReady requires the next per-origin counter and seen deps", () => {
  assert.ok(causallyReady({ a: 2, b: 1 }, { a: 1, b: 1 }, "a"));
  assert.ok(!causallyReady({ a: 3, b: 1 }, { a: 1, b: 1 }, "a"));
  assert.ok(!causallyReady({ a: 2, b: 2 }, { a: 1, b: 1 }, "a"));
});
