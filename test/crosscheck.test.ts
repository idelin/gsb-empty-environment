import assert from "node:assert/strict";
import test from "node:test";

import {
  ConditionFactory,
  attrsOf,
  checkBooleanLaws,
  contains,
  enumerateAssignments,
  evalPoint,
  exhaustiveContains,
  verifyCounterexample,
  type Assignment,
  type Cond,
  type DomainSpec,
} from "../src/index.ts";

const domain: DomainSpec = {
  r: { kind: "enum", values: ["a", "b", "c"] },
  x: { kind: "int", min: 0, max: 4 },
  y: { kind: "int", min: 0, max: 4 },
};

function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const OPS = ["<", "<=", "==", "!=", ">=", ">"] as const;

function genCond(F: ConditionFactory, rnd: () => number, depth: number): Cond {
  const pick = <T>(arr: readonly T[]): T => arr[Math.floor(rnd() * arr.length)];
  if (depth > 0 && rnd() < 0.45) {
    const kind = pick(["not", "and", "or"] as const);
    if (kind === "not") return F.not(genCond(F, rnd, depth - 1));
    const n = 2 + Math.floor(rnd() * 2);
    const args = Array.from({ length: n }, () => genCond(F, rnd, depth - 1));
    return kind === "and" ? F.and(args) : F.or(args);
  }
  const leaf = pick(["cmp", "diff", "in"] as const);
  if (leaf === "cmp") {
    return F.cmp(pick(["x", "y"]), pick(OPS), Math.floor(rnd() * 5));
  }
  if (leaf === "diff") {
    return F.diff("x", "y", pick(OPS), Math.floor(rnd() * 9) - 4);
  }
  const values = ["a", "b", "c"].filter(() => rnd() < 0.6);
  return F.in("r", values);
}

function project(a: Assignment, attrs: readonly string[]): Assignment {
  const out: Assignment = {};
  for (const k of attrs) out[k] = a[k];
  return out;
}

test("symbolic engine matches independent exhaustive checker (200 random pairs)", () => {
  const F = new ConditionFactory();
  const rnd = mulberry32(42);
  const assignments = enumerateAssignments(domain);
  let notContained = 0;
  for (let i = 0; i < 200; i += 1) {
    const outer = genCond(F, rnd, 3);
    const inner = genCond(F, rnd, 3);
    const sym = contains(F, outer, inner, domain);
    const brute = exhaustiveContains(outer, inner, domain);
    assert.equal(
      sym.status,
      brute.status,
      `pair #${i}: symbolic=${sym.status} exhaustive=${brute.status}`,
    );
    if (sym.status === "not-contained") {
      notContained += 1;
      const cex = sym.counterexample;
      // authenticity, checked independently
      assert.equal(verifyCounterexample(outer, inner, domain, cex).ok, true);
      // minimality: must equal the first counterexample in lexicographic
      // enumeration order, projected onto referenced attributes
      const attrs = [...attrsOf(outer, attrsOf(inner))].sort();
      const expected = assignments.find(
        (a) =>
          evalPoint(inner, a) === "true" && evalPoint(outer, a) === "false",
      );
      assert.ok(expected !== undefined);
      assert.deepEqual(cex, project(expected, attrs), `pair #${i} minimality`);
    }
  }
  assert.ok(notContained > 50, `expected variety, got ${notContained}`);
});

test("boolean combinations match set intersection/union/difference", () => {
  const F = new ConditionFactory();
  const rnd = mulberry32(7);
  for (let i = 0; i < 30; i += 1) {
    const a = genCond(F, rnd, 2);
    const b = genCond(F, rnd, 2);
    const report = checkBooleanLaws(
      F.and([a, b]),
      F.or([a, b]),
      F.and([a, F.not(b)]),
      a,
      b,
      domain,
    );
    assert.ok(report.andIsIntersection, `pair #${i}: and`);
    assert.ok(report.orIsUnion, `pair #${i}: or`);
    assert.ok(report.differenceIsAndNot, `pair #${i}: difference`);
  }
});

test("and/or laws hold over unknown-able domains (three-valued)", () => {
  const F = new ConditionFactory();
  const partial: DomainSpec = {
    r: { kind: "enum", values: ["a", "b"], optional: true },
    x: { kind: "int", min: 0, max: 3 },
  };
  const a = F.cmp("x", ">=", 2);
  const b = F.in("r", ["a"]);
  const report = checkBooleanLaws(
    F.and([a, b]),
    F.or([a, b]),
    F.and([a, F.not(b)]),
    a,
    b,
    partial,
  );
  // Under Kleene semantics sat(A and B) == sat(A) n sat(B) and
  // sat(A or B) == sat(A) u sat(B) still hold; the difference law does not
  // (sat(A and not B) excludes points where B is unknown).
  assert.ok(report.andIsIntersection);
  assert.ok(report.orIsUnion);
  assert.ok(!report.differenceIsAndNot);
});
