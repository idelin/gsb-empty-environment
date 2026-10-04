/**
 * Randomized cross-validation: the simplex result is checked by the
 * independent checker, and bounded instances are compared against brute-force
 * vertex enumeration.
 */
import { Frac } from "../src/fraction";
import { LP, solve } from "../src/simplex";
import { checkResult } from "../src/checker";
import { enumerateVertices } from "../src/enumerate";
import { test, assert, assertEq } from "./harness";

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomLP(rnd: () => number): LP {
  const n = Math.floor(rnd() * 5); // 0..4 variables
  const m = Math.floor(rnd() * 6); // 0..5 constraints
  const coeff = (lo: number, hi: number) =>
    Frac.of(BigInt(lo + Math.floor(rnd() * (hi - lo + 1))));
  const c: Frac[] = [];
  for (let j = 0; j < n; j++) c.push(coeff(-4, 4));
  const A: Frac[][] = [];
  const b: Frac[] = [];
  for (let i = 0; i < m; i++) {
    const row: Frac[] = [];
    for (let j = 0; j < n; j++) row.push(coeff(-3, 3));
    A.push(row);
    b.push(coeff(-5, 5));
  }
  return { c, A, b };
}

test("random: 300 small LPs, checker + vertex enumeration agree", () => {
  const rnd = mulberry32(20261004);
  const counts: Record<string, number> = {
    optimal: 0,
    infeasible: 0,
    unbounded: 0,
    budget_exhausted: 0,
  };
  for (let iter = 0; iter < 300; iter++) {
    const p = randomLP(rnd);
    const r = solve(p, 100000);
    counts[r.status]++;
    assert(r.status !== "budget_exhausted", `iter ${iter}: budget hit`);

    const check = checkResult(p, r);
    assert(
      check.ok,
      `iter ${iter}: certificate invalid: ${check.errors.join("; ")}`
    );

    const en = enumerateVertices(p);
    if (r.status === "optimal") {
      assert(en.vertices.length > 0, `iter ${iter}: no vertex but optimal`);
      assertEq(
        en.maxValue!.toString(),
        r.value!.toString(),
        `iter ${iter}: enumeration disagrees`
      );
    } else if (r.status === "infeasible") {
      assertEq(
        en.vertices.length,
        0,
        `iter ${iter}: vertices exist but solver says infeasible`
      );
    }
    // "unbounded": the ray certificate is already verified by the checker.
  }
  // The seed should produce a healthy mix of all three outcomes.
  assert(counts.optimal > 50, `too few optimal: ${JSON.stringify(counts)}`);
  assert(counts.infeasible > 10, `too few infeasible: ${JSON.stringify(counts)}`);
  assert(counts.unbounded > 10, `too few unbounded: ${JSON.stringify(counts)}`);
});

test("random: handcrafted feasible points and rays validate", () => {
  // max x+y s.t. x - y <= 1: genuinely unbounded, ray d=(1,1).
  const p: LP = {
    c: [Frac.parse("1"), Frac.parse("1")],
    A: [[Frac.parse("1"), Frac.parse("-1")]],
    b: [Frac.parse("1")],
  };
  const handmade = {
    status: "unbounded" as const,
    pivots: 0,
    x: [Frac.parse("1"), Frac.parse("1")],
    d: [Frac.parse("1"), Frac.parse("1")],
  };
  assert(checkResult(p, handmade).ok, "handcrafted ray accepted");
  const wrong1 = { ...handmade, d: [Frac.parse("0"), Frac.parse("-1")] };
  assert(!checkResult(p, wrong1).ok, "negative ray rejected");
  const wrong2 = { ...handmade, d: [Frac.parse("1"), Frac.parse("0")] };
  assert(!checkResult(p, wrong2).ok, "ray with (Ad)_1 > 0 rejected");
  const wrong3 = { ...handmade, x: [Frac.parse("-1"), Frac.parse("1")] };
  assert(!checkResult(p, wrong3).ok, "infeasible point rejected");
});
