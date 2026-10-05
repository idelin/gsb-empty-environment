import { test } from "node:test";
import assert from "node:assert/strict";
import { matchGlob } from "../src/glob.ts";

test("star matches within a single segment", () => {
  assert.equal(matchGlob("src/*.ts", "src/a.ts"), true);
  assert.equal(matchGlob("src/*.ts", "src/lib/a.ts"), false);
  assert.equal(matchGlob("src/*.ts", "src/a.js"), false);
});

test("double star matches across segments", () => {
  assert.equal(matchGlob("src/**", "src/a/b/c.ts"), true);
  assert.equal(matchGlob("**/*.ts", "a/b/c.ts"), true);
  assert.equal(matchGlob("**/*.ts", "c.ts"), true);
  assert.equal(matchGlob("a/**/b", "a/b"), true);
  assert.equal(matchGlob("a/**/b", "a/x/y/b"), true);
  assert.equal(matchGlob("a/**/b", "a/x/y/c"), false);
});

test("question mark matches one non-separator character", () => {
  assert.equal(matchGlob("src/?.ts", "src/a.ts"), true);
  assert.equal(matchGlob("src/?.ts", "src/ab.ts"), false);
  assert.equal(matchGlob("src/?.ts", "src/a/b.ts"), false);
});

test("regex specials are literal and leading slash is ignored", () => {
  assert.equal(matchGlob("src/a+b.ts", "src/a+b.ts"), true);
  assert.equal(matchGlob("src/a+b.ts", "src/aab.ts"), false);
  assert.equal(matchGlob("/src/a.ts", "src/a.ts"), true);
});
