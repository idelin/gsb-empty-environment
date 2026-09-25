import { test } from "node:test";
import assert from "node:assert/strict";
import { canonicalize } from "../src/canonicalize.ts";

test("object key order does not affect canonical form", () => {
  const a = { x: 1, y: { b: 2, a: [3, { d: 4, c: 5 }] } };
  const b = { y: { a: [3, { c: 5, d: 4 }], b: 2 }, x: 1 };
  assert.equal(canonicalize(a), canonicalize(b));
});

test("arrays preserve order in canonical form", () => {
  assert.notEqual(canonicalize([1, 2, 3]), canonicalize([3, 2, 1]));
});

test("primitives canonicalize deterministically", () => {
  assert.equal(canonicalize(null), "null");
  assert.equal(canonicalize(true), "true");
  assert.equal(canonicalize("a\"b"), JSON.stringify("a\"b"));
  assert.equal(canonicalize(-0), "0");
});
