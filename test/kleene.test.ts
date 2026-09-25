import { test } from "node:test";
import assert from "node:assert/strict";
import { FALSE, MISSING, TRUE, triAnd, triNot, triOr } from "../src/trivalue.ts";

test("strong Kleene negation", () => {
  assert.equal(triNot(TRUE), FALSE);
  assert.equal(triNot(FALSE), TRUE);
  assert.equal(triNot(MISSING), MISSING);
});

test("strong Kleene AND truth table", () => {
  assert.equal(triAnd(TRUE, TRUE), TRUE);
  assert.equal(triAnd(TRUE, FALSE), FALSE);
  assert.equal(triAnd(FALSE, MISSING), FALSE, "false dominates unknown");
  assert.equal(triAnd(TRUE, MISSING), MISSING);
  assert.equal(triAnd(MISSING, MISSING), MISSING);
});

test("strong Kleene OR truth table", () => {
  assert.equal(triOr(FALSE, FALSE), FALSE);
  assert.equal(triOr(FALSE, TRUE), TRUE);
  assert.equal(triOr(TRUE, MISSING), TRUE, "true dominates unknown");
  assert.equal(triOr(FALSE, MISSING), MISSING);
});

test("excluded middle fails with missing x", () => {
  assert.equal(triOr(MISSING, triNot(MISSING)), MISSING);
});
