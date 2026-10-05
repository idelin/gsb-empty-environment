import { test } from "node:test";
import assert from "node:assert/strict";
import { Rope } from "../src/rope.ts";

test("rope reads across chunk boundaries", () => {
  const rope = Rope.from(["foo", "bar", "baz"]);
  assert.equal(rope.length, 9);
  assert.equal(rope.toString(), "foobarbaz");
  assert.equal(rope.charAt(0), "f");
  assert.equal(rope.charAt(3), "b");
  assert.equal(rope.charAt(8), "z");
  assert.equal(rope.slice(2, 7), "obarb");
  assert.equal(rope.slice(0), "foobarbaz");
  assert.equal(rope.slice(4, 4), "");
});

test("rope rejects empty construction and out-of-bounds reads", () => {
  assert.throws(() => Rope.from([]), RangeError);
  const rope = Rope.of("ab");
  assert.throws(() => rope.charAt(2), RangeError);
  assert.throws(() => rope.charAt(-1), RangeError);
});
