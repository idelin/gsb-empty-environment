import { test } from "node:test";
import assert from "node:assert/strict";
import { tokenizeQuery } from "../src/tokenize.ts";

test("splits on whitespace runs", () => {
  assert.deepEqual(tokenizeQuery("alpha  beta\tgamma\n"), ["alpha", "beta", "gamma"]);
});

test("keeps quoted spans as one term", () => {
  assert.deepEqual(tokenizeQuery('foo "bar baz" qux'), ["foo", "bar baz", "qux"]);
});

test("empty and whitespace-only queries yield no tokens", () => {
  assert.deepEqual(tokenizeQuery(""), []);
  assert.deepEqual(tokenizeQuery("   "), []);
});

test("unterminated quote consumes the rest", () => {
  assert.deepEqual(tokenizeQuery('a "b c'), ["a", "b c"]);
});
