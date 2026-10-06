import assert from "node:assert/strict";
import test from "node:test";

import { formatExpr } from "../src/ast.ts";
import {
  checkFormula,
  dimFromUnitExpr,
  minimalFailure,
  renderJudgment,
  type Env,
} from "../src/check.ts";
import { parseFormula } from "../src/parser.ts";
import { dimEqual, formatDim, isDimensionless } from "../src/units.ts";

const BASES = ["kg", "m", "s"] as const;

function envOf(vars: Record<string, string>): Env {
  const env: Record<string, ReturnType<typeof dimFromUnitExpr>> = {};
  for (const [name, unit] of Object.entries(vars)) {
    env[name] = dimFromUnitExpr(unit, BASES);
  }
  return env;
}

// v: velocity, t: time, a: acceleration, d: distance, m0: mass
const ENV = envOf({
  v: "m/s",
  t: "s",
  a: "m/s^2",
  d: "m",
  m0: "kg",
});

function dimOf(src: string, env: Env = ENV) {
  const judgment = checkFormula(src, env);
  assert.equal(
    minimalFailure(judgment),
    null,
    `expected '${src}' to check, got: ${renderJudgment(judgment)}`,
  );
  return judgment.dim!;
}

test("kinematics: v*t + 0.5*a*t^2 has unit m", () => {
  const dim = dimOf("v*t + 0.5*a*t^2");
  assert.equal(formatDim(dim), "m");
});

test("derivation tree records every rule application", () => {
  const judgment = checkFormula("v*t + 0.5*a*t^2", ENV);
  const tree = renderJudgment(judgment);
  const rules = [...tree.matchAll(/^\s*\[(\w+)\]/gm)].map((m) => m[1]);
  assert.deepEqual(rules, [
    "Add",
    "Mul", "Var", "Var",
    "Mul",
    "Mul", "Num", "Var",
    "Pow", "Var",
  ]);
  assert.match(tree, /\[Add\] .* : m$/m);
  assert.match(tree, /\[Pow\] t\^2 : s\^2/m);
});

test("parentheses change grouping and hence units", () => {
  // (d + d) * t : m*s ...
  assert.equal(formatDim(dimOf("(d + d) * t")), "m*s");
  // ... while d + d * t is ill-typed (m vs m*s)
  const judgment = checkFormula("d + d * t", ENV);
  const failure = minimalFailure(judgment);
  assert.ok(failure);
  assert.equal(formatExpr(failure.expr), "d + d * t");
});

test("parenthesized sum as power base: (v + v)^2", () => {
  assert.equal(formatDim(dimOf("(v + v)^2")), "m^2/s^2");
});

test("formatExpr round-trips with minimal parentheses", () => {
  for (const src of [
    "(a + b) * c",
    "a + b * c",
    "a / (b * c)",
    "(a + b)^2",
    "x^(-2)",
  ]) {
    assert.equal(formatExpr(parseFormula(src)), src);
  }
});

test("negative powers invert dimensions", () => {
  assert.equal(formatDim(dimOf("t^-1")), "1/s");
  assert.equal(formatDim(dimOf("t^(-2)")), "1/s^2");
  assert.equal(formatDim(dimOf("v^-2")), "s^2/m^2");
  // x / y^2 === x * y^-2
  assert.ok(dimEqual(dimOf("d / t^2"), dimOf("d * t^-2")));
});

test("dimensionless results", () => {
  for (const src of ["d / d", "v * v^-1", "t^0", "2 + 3", "d / (2 * d)"]) {
    assert.ok(isDimensionless(dimOf(src)), `expected '${src}' dimensionless`);
  }
  assert.equal(formatDim(dimOf("d / d")), "1");
});

test("addition requires identical units", () => {
  const judgment = checkFormula("v + t", ENV);
  const failure = minimalFailure(judgment);
  assert.ok(failure);
  assert.match(failure.error!, /unit mismatch in addition: m\/s vs s/);
  assert.equal(formatExpr(failure.expr), "v + t");
});

test("minimal failing subexpression is the deepest local error", () => {
  // The whole formula is ill-typed, but the minimal culprit is `v + t`.
  const judgment = checkFormula("(v + t) * m0 + d", ENV);
  const failure = minimalFailure(judgment);
  assert.ok(failure);
  assert.equal(formatExpr(failure.expr), "v + t");
  assert.match(failure.error!, /unit mismatch/);
  // The rendered tree annotates the error only at that node.
  const tree = renderJudgment(judgment);
  const annotated = tree.split("\n").filter((line) => line.includes("--"));
  assert.equal(annotated.length, 1);
  assert.match(annotated[0], /\[Add\] v \+ t : ✗/);
});

test("zero constant is not implicitly converted to any unit", () => {
  for (const src of ["d + 0", "0 + d", "d + 0.0", "d + 0 * t"]) {
    const judgment = checkFormula(src, ENV);
    const failure = minimalFailure(judgment);
    assert.ok(failure, `expected '${src}' to fail`);
    assert.match(failure.error!, /unit mismatch in addition/);
  }
  const direct = minimalFailure(checkFormula("d + 0", ENV));
  assert.match(direct!.error!, /never implicitly converted/);
  // But zero is fine in dimensionless and multiplicative contexts.
  assert.ok(isDimensionless(dimOf("0 + 0")));
  assert.equal(formatDim(dimOf("0 * d")), "m");
});

test("unknown variables are reported at the Var node", () => {
  const judgment = checkFormula("d + oops", ENV);
  const failure = minimalFailure(judgment);
  assert.ok(failure);
  assert.equal(failure.rule, "Var");
  assert.match(failure.error!, /unknown variable 'oops'/);
});

test("exponents must be integer literals", () => {
  assert.throws(() => parseFormula("x^1.5"), /integer literal/);
  assert.throws(() => parseFormula("x^y"), /integer literal/);
  assert.throws(() => parseFormula("-x"), /unary minus/);
});

test("unit expressions: negative powers and dimensionless", () => {
  assert.equal(formatDim(dimFromUnitExpr("kg*m/s^2", BASES)), "kg*m/s^2");
  assert.equal(formatDim(dimFromUnitExpr("m/s", BASES)), "m/s");
  assert.ok(isDimensionless(dimFromUnitExpr("m/m", BASES)));
  assert.throws(() => dimFromUnitExpr("m + s", BASES), /invalid unit expression/);
});
