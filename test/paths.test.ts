import { test } from "node:test";
import assert from "node:assert/strict";
import { formatPath, matchRule, parseRulePath } from "../src/paths.ts";

test("parses dotted, wildcard and indexed rule paths", () => {
  assert.deepEqual(parseRulePath("users.*.email"), ["users", "*", "email"]);
  assert.deepEqual(parseRulePath("$.users[].email"), ["users", "*", "email"]);
  assert.deepEqual(parseRulePath("a.b[0].c"), ["a", "b", "0", "c"]);
  assert.deepEqual(parseRulePath("$"), []);
});

test("matches wildcard against object keys and array indices", () => {
  const rule = parseRulePath("users.*.email");
  assert.ok(matchRule(rule, ["users", 0, "email"]));
  assert.ok(matchRule(rule, ["users", 12, "email"]));
  assert.ok(!matchRule(rule, ["users", 0, "name"]));
  assert.ok(!matchRule(rule, ["users", 0]));
});

test("formats paths for error reporting", () => {
  assert.equal(formatPath(["users", 3, "email"]), "$.users[3].email");
  assert.equal(formatPath([]), "$");
  assert.equal(formatPath(["odd key"]), '$["odd key"]');
});
