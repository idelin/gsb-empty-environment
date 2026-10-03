/**
 * Automatic search for a minimal counterexample over finite rational
 * inputs. Candidates are exact rationals p/q enumerated by height
 * (|p| + q) so the first divergence found is minimal in that order.
 * A hard budget bounds evaluations; if it runs out before the space is
 * exhausted the result is explicitly "undecided".
 */
import type { Pipeline } from "./dag.ts";
import { comparePipelines, type CompareReport } from "./compare.ts";
import { Rational } from "./rational.ts";

export interface SearchSpace {
  slots: string[];
  /** |numerator| <= maxNum, 1 <= denominator <= maxDen. */
  maxNum: number;
  maxDen: number;
  includeNegative?: boolean; // default true
  includeZero?: boolean; // default true
}

export interface SearchReport {
  status:
    | "counterexample-found" // minimal (by height order) divergent input
    | "no-divergence-in-space" // finite space fully enumerated, all equal
    | "undecided-budget-exhausted"; // budget ran out, space not exhausted
  budget: number;
  tested: number;
  totalCandidates: number; // size of the finite space
  counterexample?: Record<string, string>;
  compareReport?: CompareReport;
}

/** All reduced rationals p/q with |p|<=maxNum, q<=maxDen, ordered by height. */
export function* enumerateRationals(space: SearchSpace): Generator<Rational> {
  const includeNeg = space.includeNegative ?? true;
  const includeZero = space.includeZero ?? true;
  const maxHeight = space.maxNum + space.maxDen;
  for (let h = includeZero ? 1 : 2; h <= maxHeight; h++) {
    for (let q = 1; q <= space.maxDen; q++) {
      const p = h - q;
      if (p < 0 || p > space.maxNum) continue;
      if (p === 0) {
        if (includeZero && q === 1) yield Rational.of(0n, 1n);
        continue;
      }
      if (gcd(p, q) !== 1) continue;
      yield Rational.of(BigInt(p), BigInt(q));
      if (includeNeg) yield Rational.of(BigInt(-p), BigInt(q));
    }
  }
}

function gcd(a: number, b: number): number {
  while (b) {
    const t = a % b;
    a = b;
    b = t;
  }
  return a;
}

export function spaceSize(space: SearchSpace): number {
  const includeNeg = space.includeNegative ?? true;
  const includeZero = space.includeZero ?? true;
  let count = includeZero ? 1 : 0;
  for (let p = 1; p <= space.maxNum; p++) {
    for (let q = 1; q <= space.maxDen; q++) {
      if (gcd(p, q) === 1) count += includeNeg ? 2 : 1;
    }
  }
  return count;
}

/**
 * Enumerate input tuples ordered by the sum of per-slot enumeration
 * indices (diagonal order), so the first hit is minimal.
 */
function* enumerateTuples(space: SearchSpace): Generator<Record<string, Rational>> {
  const perSlot = space.slots.map(() => [...enumerateRationals(space)]);
  const k = space.slots.length;
  if (k === 0) {
    yield {};
    return;
  }
  const total = perSlot[0].length;
  // diagonal enumeration over k-tuples of indices in [0,total)
  for (let sum = 0; sum <= k * (total - 1); sum++) {
    const idx = new Array<number>(k).fill(0);
    const rec = function* (slot: number, remaining: number): Generator<number[]> {
      if (slot === k - 1) {
        if (remaining < total) {
          idx[slot] = remaining;
          yield [...idx];
        }
        return;
      }
      for (let i = Math.min(remaining, total - 1); i >= 0; i--) {
        idx[slot] = i;
        yield* rec(slot + 1, remaining - i);
      }
    };
    for (const combo of rec(0, sum)) {
      const tuple: Record<string, Rational> = {};
      for (let s = 0; s < k; s++) tuple[space.slots[s]] = perSlot[s][combo[s]];
      yield tuple;
    }
  }
}

export function searchMinimalCounterexample(
  pa: Pipeline,
  pb: Pipeline,
  space: SearchSpace,
  budget: number
): SearchReport {
  const perSlot = spaceSize(space);
  const totalCandidates = perSlot ** space.slots.length;
  let tested = 0;
  for (const tuple of enumerateTuples(space)) {
    if (tested >= budget) {
      return { status: "undecided-budget-exhausted", budget, tested, totalCandidates };
    }
    tested++;
    const report = comparePipelines(pa, pb, tuple);
    if (report.status === "output-divergence" || report.status === "intermediate-divergence") {
      const counterexample: Record<string, string> = {};
      for (const [k, v] of Object.entries(tuple)) counterexample[k] = v.toString();
      return {
        status: "counterexample-found",
        budget,
        tested,
        totalCandidates,
        counterexample,
        compareReport: report,
      };
    }
  }
  return { status: "no-divergence-in-space", budget, tested, totalCandidates };
}
