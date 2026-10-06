import { test } from "node:test";
import assert from "node:assert/strict";
import { parse } from "../src/parser.ts";
import { check, formatMinimalErrors, type Env } from "../src/checker.ts";
import {
  DIMENSIONLESS,
  divUnits,
  formatUnit,
  isDimensionless,
  mulUnits,
  powUnit,
  unitOf,
  unitsEqual,
} from "../src/units.ts";

const env: Env = new Map([
  ["v", unitOf({ L: 1, T: -1 })],
  ["t", unitOf({ T: 1 })],
  ["d", unitOf({ L: 1 })],
  ["m", unitOf({ M: 1 })],
  ["a", unitOf({ L: 1, T: -2 })],
]);

const infer = (src: string, e: Env = env) => check(parse(src), e);

// ---------- algebraic exponent model ----------

test("mul/div/pow compose as vector add/sub/scale", () => {
  const L = unitOf({ L: 1 });
  const T = unitOf({ T: 1 });
  assert.ok(unitsEqual(mulUnits(L, T), unitOf({ L: 1, T: 1 })));
  assert.ok(unitsEqual(divUnits(L, T), unitOf({ L: 1, T: -1 })));
  assert.ok(unitsEqual(powUnit(unitOf({ L: 1, T: -1 }), 2), unitOf({ L: 2, T: -2 })));
});

test("negative powers invert the vector", () => {
  const u = unitOf({ L: 1, T: -2 });
  assert.ok(unitsEqual(powUnit(u, -1), unitOf({ L: -1, T: 2 })));
  assert.ok(unitsEqual(powUnit(u, 0), DIMENSIONLESS));
});

test("dimensionless is the zero vector", () => {
  assert.ok(isDimensionless(DIMENSIONLESS));
  assert.ok(isDimensionless(mulUnits(unitOf({ L: 1 }), unitOf({ L: -1 }))));
});

// ---------- checker: happy paths ----------

test("addition with equal units checks", () => {
  const r = infer("v * t + d");
  assert.equal(formatUnit(r.unit!), "L");
});

test("parentheses change grouping and still check", () => {
  const r = infer("(v / a)^2 * m");
  assert.equal(formatUnit(r.unit!), "M*T^2");
  const r2 = infer("v / (a * t)");
  assert.ok(isDimensionless(r2.unit!));
});

test("negative power in formula", () => {
  const r = infer("1 / t^-1");
  assert.equal(formatUnit(r.unit!), "T");
  const r2 = infer("(d / t^2)^-1");
  assert.equal(formatUnit(r2.unit!), "T^2/L");
});

test("dimensionless results", () => {
  assert.ok(isDimensionless(infer("d / d").unit!));
  assert.ok(isDimensionless(infer("(v * t) / d").unit!));
  assert.ok(isDimensionless(infer("3.5").unit!));
});

// ---------- checker: failures ----------

test("addition requires identical units", () => {
  const r = infer("v * t + m");
  assert.equal(r.unit, null);
  assert.equal(r.minimalErrors.length, 1);
  assert.equal(r.minimalErrors[0]!.error!.code, "UNIT_MISMATCH");
});

test("zero constant is not implicitly coerced to any unit", () => {
  const r = infer("v + 0");
  assert.equal(r.unit, null);
  const err = r.minimalErrors[0]!.error!;
  assert.equal(err.code, "UNIT_MISMATCH");
  if (err.code === "UNIT_MISMATCH") {
    assert.ok(isDimensionless(err.right));
    assert.equal(formatUnit(err.left), "L/T");
  }
  // subtraction too
  assert.equal(infer("d - 0").unit, null);
  // but dimensionless + 0 is fine
  assert.ok(isDimensionless(infer("(d / d) + 0").unit!));
});

test("undefined variable is reported", () => {
  const r = infer("v + q");
  assert.equal(r.unit, null);
  assert.equal(r.minimalErrors[0]!.error!.code, "UNDEFINED_VAR");
});

// ---------- minimal erroring subexpression ----------

test("minimal error is the innermost failing node, not the whole formula", () => {
  const src = "(v + m) * a / d";
  const r = infer(src);
  assert.equal(r.minimalErrors.length, 1);
  const node = r.minimalErrors[0]!;
  assert.equal(src.slice(node.expr.start, node.expr.end), "v + m");
  // ancestors carry PROPAGATED, not the root cause
  assert.equal(r.tree.error!.code, "PROPAGATED");
});

test("multiple independent minimal errors are all reported", () => {
  const src = "(v + m) * (d + t)";
  const r = infer(src);
  assert.equal(r.minimalErrors.length, 2);
  const texts = r.minimalErrors.map((n) => src.slice(n.expr.start, n.expr.end));
  assert.deepEqual(texts, ["v + m", "d + t"]);
});

test("formatMinimalErrors renders span, text and reason", () => {
  const src = "v + m";
  const out = formatMinimalErrors(src, infer(src));
  assert.match(out, /\[0:5\] 'v \+ m' — cannot \+ L\/T and M/);
});

// ---------- derivation tree ----------

test("derivation tree mirrors expression structure", () => {
  const r = infer("(v * t) + d");
  assert.equal(r.tree.children.length, 2);
  const mul = r.tree.children[0]!;
  assert.equal(mul.children.length, 2);
  assert.equal(mul.children[0]!.rule, "v : L/T");
  assert.equal(r.tree.rule, "+(L, L) => L");
});
