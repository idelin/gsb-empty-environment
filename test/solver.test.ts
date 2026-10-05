import { test } from "node:test";
import assert from "node:assert/strict";
import type { Constraint } from "../src/types.ts";
import {
  solve,
  enumerateValidConfigs,
  configMask,
  isValidConfig,
  popcount,
  comboBit,
} from "../src/solver.ts";
import { explainUnreachable } from "../src/explain.ts";
import { maxCoveringFamily } from "../src/bounds.ts";

/** Independent minimality check: try every subset of valid configs. */
function bruteForceMin(n: number, constraints: Constraint[]): number {
  const valid = enumerateValidConfigs(n, constraints);
  const masks = valid.map((c) => configMask(c, n));
  let reachable = 0n;
  for (const m of masks) reachable |= m;
  if (reachable === 0n) return 0;
  const existsCover = (k: number): boolean => {
    const rec = (start: number, left: number, union: bigint): boolean => {
      if (left === 0) return (union & reachable) === reachable;
      for (let i = start; i <= masks.length - left; i++) {
        if (rec(i + 1, left - 1, union | masks[i])) return true;
      }
      return false;
    };
    return rec(0, k, 0n);
  };
  for (let k = 1; k <= valid.length; k++) {
    if (existsCover(k)) return k;
  }
  throw new Error("unreachable: all-zero config is always valid");
}

function checkResult(n: number, constraints: Constraint[]): void {
  const r = solve(n, constraints);
  // Every emitted config is legal (illegal configs never count).
  for (const c of r.configs) assert.ok(isValidConfig(c, constraints));
  // All reachable pairs covered.
  let covered = 0n;
  for (const c of r.configs) covered |= configMask(c, n);
  assert.equal((covered & r.reachable) & ~r.reachable, 0n);
  assert.equal((covered & r.reachable), r.reachable);
  assert.ok(r.verified);
  // Minimality, independently verified.
  assert.equal(r.minCount, bruteForceMin(n, constraints));
}

test("two features with mutex need 3 configs", () => {
  const cons: Constraint[] = [{ type: "mutex", a: 0, b: 1 }];
  const r = solve(2, cons);
  assert.equal(r.minCount, 3);
  assert.equal(r.minCount, bruteForceMin(2, cons));
  // (1,1) is the only unreachable combo and is reported as a direct violation.
  const un = explainUnreachable(2, cons, r.reachable, ["A", "B"]);
  assert.equal(un.length, 1);
  assert.deepEqual([un[0].i, un[0].j, un[0].vi, un[0].vj], [0, 1, 1, 1]);
  assert.equal(un[0].kind, "direct");
});

test("unconstrained 3 features need 4 configs", () => {
  const r = solve(3, []);
  assert.equal(r.minCount, 4);
  assert.equal(r.minCount, bruteForceMin(3, []));
});

test("unconstrained 4 features need 5 configs", () => {
  const r = solve(4, []);
  assert.equal(r.minCount, 5);
  assert.equal(r.minCount, bruteForceMin(4, []));
});

test("implies chain: unreachable combo explained by propagation", () => {
  // A=>C, B=>D, mutex(C,D): (A=1,B=1) forces C=1 and D=1, violating mutex.
  const cons: Constraint[] = [
    { type: "implies", from: 0, to: 2 },
    { type: "implies", from: 1, to: 3 },
    { type: "mutex", a: 2, b: 3 },
  ];
  const r = solve(4, cons);
  assert.equal(r.minCount, bruteForceMin(4, cons));
  const un = explainUnreachable(4, cons, r.reachable, ["A", "B", "C", "D"]);
  const ab = un.find((u) => u.i === 0 && u.j === 1 && u.vi === 1 && u.vj === 1);
  assert.ok(ab);
  assert.equal(ab.kind, "propagation");
  assert.match(ab.explanation, /矛盾/);
  // Every unreachable combo gets an explanation.
  for (const u of un) assert.ok(u.explanation.length > 0);
  // Unreachable combos are disjoint from coverage accounting.
  const totalPairs = (4 * 3) / 2;
  assert.equal(r.reachableCount + un.length, totalPairs * 4);
});

test("random constrained instances match brute force", () => {
  let seed = 12345;
  const rnd = (): number => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x7fffffff;
  };
  for (let t = 0; t < 20; t++) {
    const n = 2 + Math.floor(rnd() * 5); // 2..6 features
    const cons: Constraint[] = [];
    const m = Math.floor(rnd() * 6);
    for (let c = 0; c < m; c++) {
      const a = Math.floor(rnd() * n);
      let b = Math.floor(rnd() * n);
      if (b === a) b = (b + 1) % n;
      if (rnd() < 0.5) cons.push({ type: "mutex", a, b });
      else cons.push({ type: "implies", from: a, to: b });
    }
    checkResult(n, cons);
  }
});

test("single feature needs no configs", () => {
  const r = solve(1, []);
  assert.equal(r.minCount, 0);
  assert.equal(r.reachableCount, 0);
});

test("maxCoveringFamily matches known values", () => {
  assert.equal(maxCoveringFamily(3), 1);
  assert.equal(maxCoveringFamily(4), 3);
  assert.equal(maxCoveringFamily(5), 4);
  assert.equal(maxCoveringFamily(6), 10);
  assert.equal(maxCoveringFamily(7), 15);
});

test("unconstrained 12 features need 7 configs (structural bound)", () => {
  const r = solve(12, []);
  assert.equal(r.minCount, 7);
  assert.equal(r.lowerBound, 7);
  assert.equal(r.structural.bound, 7);
  assert.ok(r.verified);
});

test("reachable combos never include illegal-only pairs", () => {
  // mutex makes (1,1) unreachable; it must not appear in the reachable mask.
  const cons: Constraint[] = [{ type: "mutex", a: 0, b: 1 }];
  const r = solve(2, cons);
  assert.equal((r.reachable & comboBit(2, 0, 1, 1, 1)) === 0n, true);
  assert.equal(popcount(r.reachable), 3);
});
