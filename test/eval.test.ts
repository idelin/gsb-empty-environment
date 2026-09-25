import assert from "node:assert/strict";
import test from "node:test";
import { and, cmp, lit, not, or, resetIds, type Expr, type Tri } from "../src/ast.ts";
import { evalExpr } from "../src/eval.ts";

resetIds();

const T: Expr = lit(true);
const F: Expr = lit(false);
const U: Expr = cmp("missing", ">=", 1); // unknown: attribute never provided

test("NOT truth table (Kleene)", () => {
  assert.equal(evalExpr(not(T), {}), "false");
  assert.equal(evalExpr(not(F), {}), "true");
  assert.equal(evalExpr(not(U), {}), "unknown");
});

test("AND truth table (Kleene)", () => {
  const cases: [Expr, Expr, Tri][] = [
    [T, T, "true"],
    [T, F, "false"],
    [T, U, "unknown"],
    [F, T, "false"],
    [F, F, "false"],
    [F, U, "false"], // false dominates
    [U, T, "unknown"],
    [U, F, "false"],
    [U, U, "unknown"],
  ];
  for (const [a, b, want] of cases) {
    assert.equal(evalExpr(and(a, b), {}), want, `AND(${want})`);
  }
});

test("OR truth table (Kleene)", () => {
  const cases: [Expr, Expr, Tri][] = [
    [T, T, "true"],
    [T, F, "true"],
    [T, U, "true"], // true dominates
    [F, T, "true"],
    [F, F, "false"],
    [F, U, "unknown"],
    [U, T, "true"],
    [U, F, "unknown"],
    [U, U, "unknown"],
  ];
  for (const [a, b, want] of cases) {
    assert.equal(evalExpr(or(a, b), {}), want, `OR(${want})`);
  }
});

test("missing attribute makes its condition unknown, present attribute decides", () => {
  const ageGe18 = cmp("age", ">=", 18);
  assert.equal(evalExpr(ageGe18, {}), "unknown");
  assert.equal(evalExpr(ageGe18, { age: 17 }), "false");
  assert.equal(evalExpr(ageGe18, { age: 18 }), "true");
});

test("excluded middle is unknown (not true) when the attribute is missing", () => {
  const x = cmp("level", ">=", 3);
  assert.equal(evalExpr(or(x, not(x)), {}), "unknown");
  assert.equal(evalExpr(and(x, not(x)), {}), "unknown");
});
