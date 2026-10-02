/**
 * Contract containment: does every value valid under schema B also satisfy
 * schema A? Decided on the exact lattice model used by the solver. When
 * containment fails, a minimal counterexample (smallest |value|, exact) is
 * produced and verified: valid under B, invalid under A.
 */

import type { Decimal, DecimalOptions } from "./decimal.ts";
import {
  absDecimal,
  canonical,
  compareDecimal,
  fromScaledInt,
  isMultipleOf,
  parseDecimal,
  precisionOf,
  scaleOf,
  zeroDecimal,
} from "./decimal.ts";
import type { Bound, Constraints, RawNumberSchema } from "./constraints.ts";
import { checkConstraints, normalizeConstraints } from "./constraints.ts";
import type { LatticeInfo } from "./solver.ts";
import { latticeInfo, solve } from "./solver.ts";

export interface Counterexample {
  readonly value: Decimal;
  readonly canonical: string;
  /** The aspect of A that the value violates. */
  readonly violates: string;
}

export interface ContainmentResult {
  /** true = B ⊆ A proved; false = counterexample found; "undetermined" = search budget hit. */
  readonly contained: boolean | "undetermined";
  readonly reason: string;
  readonly counterexample?: Counterexample;
}

const COUNTEREXAMPLE_SCAN_BUDGET = 100_000;

function gcd(a: bigint, b: bigint): bigint {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x;
}

function ceilDiv(a: bigint, b: bigint): bigint {
  const q = a / b;
  const r = a % b;
  return r !== 0n && a > 0n ? q + 1n : q;
}

function floorDiv(a: bigint, b: bigint): bigint {
  const q = a / b;
  const r = a % b;
  return r !== 0n && a < 0n ? q - 1n : q;
}

function withLo(c: Constraints, b: Bound): Constraints {
  if (c.lo === undefined) return { ...c, lo: b };
  const cmp = compareDecimal(c.lo.value, b.value);
  if (cmp < 0) return { ...c, lo: b };
  if (cmp > 0) return c;
  return { ...c, lo: c.lo.inclusive ? b : c.lo };
}

function withHi(c: Constraints, b: Bound): Constraints {
  if (c.hi === undefined) return { ...c, hi: b };
  const cmp = compareDecimal(c.hi.value, b.value);
  if (cmp > 0) return { ...c, hi: b };
  if (cmp < 0) return c;
  return { ...c, hi: c.hi.inclusive ? b : c.hi };
}

function trailingZeros(v: bigint): number {
  let n = v < 0n ? -v : v;
  let t = 0;
  while (n !== 0n && n % 10n === 0n) {
    n /= 10n;
    t++;
  }
  return t;
}

/** k-candidates in [kLo, kHi] ordered by increasing |k| (0 first). */
function* kByMagnitude(kLo: bigint | null, kHi: bigint | null): Generator<bigint> {
  const loBad = kLo === null || kLo <= 0n;
  const hiBad = kHi === null || kHi >= 0n;
  if (loBad && hiBad) {
    yield 0n;
    for (let i = 1; i <= COUNTEREXAMPLE_SCAN_BUDGET; i++) {
      const k = BigInt(i);
      if (kHi === null || k <= kHi) yield k;
      if (kLo === null || -k >= kLo) yield -k;
      if (kHi !== null && k > kHi && kLo !== null && -k < kLo) return;
    }
    return;
  }
  const start = kLo !== null && kLo > 0n ? kLo : kHi!;
  const step = kLo !== null && kLo > 0n ? 1n : -1n;
  let k = start;
  for (let i = 0; i <= COUNTEREXAMPLE_SCAN_BUDGET; i++) {
    if (kLo !== null && k < kLo) return;
    if (kHi !== null && k > kHi) return;
    yield k;
    k += step;
  }
}

interface ModeHit {
  readonly value: Decimal;
  readonly violates: string;
}

/** Decide containment of B's contract within A's contract. */
export function contains(
  A: RawNumberSchema | Constraints,
  B: RawNumberSchema | Constraints,
  opts: DecimalOptions = {},
): ContainmentResult {
  const a: Constraints = isNormalized(A) ? A : normalizeConstraints(A as RawNumberSchema, opts, "#");
  const b: Constraints = isNormalized(B) ? B : normalizeConstraints(B as RawNumberSchema, opts, "#");

  const sB = solve(b, opts);
  if (!sB.feasible) {
    return {
      contained: true,
      reason: `B is infeasible, so it is vacuously contained: ${sB.evidence}`,
    };
  }

  const hits: ModeHit[] = [];
  let undetermined: string | null = null;
  const notes: string[] = [];

  // Mode 1/2: range violations, decided exactly by the solver.
  if (a.lo !== undefined) {
    const r = solve(withHi(b, { value: a.lo.value, inclusive: !a.lo.inclusive }), opts);
    if (r.feasible) hits.push({ value: r.witness, violates: "minimum" });
    else notes.push("lower range contained");
  }
  if (a.hi !== undefined) {
    const r = solve(withLo(b, { value: a.hi.value, inclusive: !a.hi.inclusive }), opts);
    if (r.feasible) hits.push({ value: r.witness, violates: "maximum" });
    else notes.push("upper range contained");
  }

  const latB = latticeInfo(b, opts);

  // Mode 3: multipleOf violation.
  if (a.multipleOf !== undefined) {
    const mA = a.multipleOf;
    const hit = multipleOfCounterexample(b, latB, mA, opts);
    if (hit === "infeasible") notes.push(`every B value is a multiple of ${canonical(mA)}`);
    else if (hit === "undetermined") undetermined = "multipleOf counterexample search exhausted";
    else if (hit !== null) hits.push({ value: hit, violates: "multipleOf" });
  }

  // Mode 4: precision violation.
  if (a.precision !== undefined) {
    const pA = a.precision;
    const hit = precisionCounterexample(b, latB, pA, opts);
    if (hit === "infeasible") notes.push(`B precision never exceeds ${pA}`);
    else if (hit === "undetermined") undetermined = "precision counterexample search exhausted";
    else if (hit !== null) hits.push({ value: hit, violates: "precision" });
  }

  // Mode 5: scale violation.
  if (a.scale !== undefined) {
    const sA = a.scale;
    const hit = scaleCounterexample(b, latB, sA, opts);
    if (hit === "infeasible") notes.push(`B scale never exceeds ${sA}`);
    else if (hit === "undetermined") undetermined = "scale counterexample search exhausted";
    else if (hit !== null) hits.push({ value: hit, violates: "scale" });
  }

  if (hits.length > 0) {
    // Minimal destructive counterexample: smallest |value|, ties smaller value.
    let best = hits[0]!;
    for (const h of hits) {
      const cmp = compareDecimal(absDecimal(h.value), absDecimal(best.value));
      if (cmp < 0 || (cmp === 0 && compareDecimal(h.value, best.value) < 0)) best = h;
    }
    // Verify: valid under B, invalid under A.
    const okB = checkConstraints(best.value, b).length === 0;
    const badA = checkConstraints(best.value, a).length > 0;
    if (!okB || !badA) {
      throw new Error("internal containment error: counterexample failed verification");
    }
    return {
      contained: false,
      reason: `B is not contained in A: ${canonical(best.value)} satisfies B but violates A's ${best.violates}`,
      counterexample: { value: best.value, canonical: canonical(best.value), violates: best.violates },
    };
  }
  if (undetermined !== null) {
    return { contained: "undetermined", reason: undetermined };
  }
  return {
    contained: true,
    reason: `every value valid under B is valid under A (${notes.join("; ") || "no extra constraints in A"})`,
  };
}

const RAW_KEYS = ["minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "precision", "scale"];

function isNormalized(x: RawNumberSchema | Constraints): x is Constraints {
  // A normalized Constraints object never carries raw schema keywords, and its
  // multipleOf (if any) is a Decimal object rather than a string/number.
  const r = x as Record<string, unknown>;
  if (RAW_KEYS.some((k) => k in r)) return false;
  return r.multipleOf === undefined || typeof r.multipleOf === "object";
}

type ModeResult = Decimal | "infeasible" | "undetermined" | null;

function multipleOfCounterexample(
  b: Constraints,
  latB: LatticeInfo | null,
  mA: Decimal,
  opts: DecimalOptions,
): ModeResult {
  if (latB === null) {
    // Continuous B: any non-multiple inside the range is a counterexample.
    const w = solve(b, opts);
    if (!w.feasible) return "infeasible";
    const candidates: Decimal[] = [w.witness];
    if (b.lo !== undefined) candidates.push(b.lo.value);
    if (b.hi !== undefined) candidates.push(b.hi.value);
    candidates.push(fromScaledInt(w.witness.coeff * 10n + 1n, w.witness.exp - 1));
    for (const v of candidates) {
      if (checkConstraints(v, b).length === 0 && !isMultipleOf(v, mA)) return v;
    }
    return "infeasible"; // single-point range equal to a multiple
  }
  const { g, q, jLo, jHi } = latB;
  const L = fromScaledInt(q, -g); // lattice step as a decimal
  if (isMultipleOf(L, mA)) return "infeasible"; // every lattice point is a multiple
  // v = k*L is a multiple of mA iff k ≡ 0 (mod T), T = ma / gcd(lb, ma).
  const E = Math.max(g, scaleOf(mA));
  const lb = q * 10n ** BigInt(E - g);
  const ma = mA.coeff * 10n ** BigInt(mA.exp + E);
  const T = ma / gcd(lb, ma);
  const kLo = jLo === null ? null : ceilDiv(jLo, q);
  const kHi = jHi === null ? null : floorDiv(jHi, q);
  for (const k of kByMagnitude(kLo, kHi)) {
    if (k === 0n || k % T === 0n) continue;
    const v = fromScaledInt(k * q, -g);
    if (checkConstraints(v, b).length === 0) return v;
  }
  return kLo !== null && kHi !== null ? "infeasible" : "undetermined";
}

function precisionCounterexample(
  b: Constraints,
  latB: LatticeInfo | null,
  pA: number,
  opts: DecimalOptions,
): ModeResult {
  const found: Decimal[] = [];
  let undetermined = false;

  // (a) values with too many integer digits: |v| >= 10^pA.
  const big = parseDecimal("1e" + pA, opts);
  for (const sub of [withLo(b, { value: big, inclusive: true }), withHi(b, { value: { ...big, neg: true }, inclusive: true })]) {
    const r = solve(sub, opts);
    if (r.feasible && precisionOf(r.witness) > pA) found.push(r.witness);
  }

  // (b) sub-1 values with too many fractional digits.
  if (latB !== null) {
    const maxScale = latB.g - trailingZeros(latB.q);
    if (maxScale > pA) {
      const kLo = latB.jLo === null ? null : ceilDiv(latB.jLo, latB.q);
      const kHi = latB.jHi === null ? null : floorDiv(latB.jHi, latB.q);
      for (const k of kByMagnitude(kLo, kHi)) {
        if (k === 0n) continue;
        const v = fromScaledInt(k * latB.q, -latB.g);
        if (precisionOf(v) > pA && checkConstraints(v, b).length === 0) {
          found.push(v);
          break;
        }
      }
      if (found.length === 0 && (kLo === null || kHi === null)) undetermined = true;
    }
  } else {
    // Continuous: fractional digits unbounded below 1 if the range reaches there.
    const tiny = parseDecimal("1e-" + (pA + 1), opts);
    for (const v of [tiny, { ...tiny, neg: true }]) {
      if (checkConstraints(v, b).length === 0) {
        found.push(v);
        break;
      }
    }
  }

  if (found.length === 0) return undetermined ? "undetermined" : "infeasible";
  let best = found[0]!;
  for (const v of found) {
    if (compareDecimal(absDecimal(v), absDecimal(best)) < 0) best = v;
  }
  return best;
}

function scaleCounterexample(
  b: Constraints,
  latB: LatticeInfo | null,
  sA: number,
  opts: DecimalOptions,
): ModeResult {
  if (latB === null) {
    // Continuous B admits any scale; find a value with scale sA+1 in range.
    const w = solve(b, opts);
    if (!w.feasible) return "infeasible";
    const e = Math.min(w.witness.exp, -(sA + 1));
    const shifted =
      w.witness.coeff * 10n ** BigInt(w.witness.exp - e) + 10n ** BigInt(-(sA + 1) - e);
    for (const cand of [fromScaledInt(shifted, e), fromScaledInt(shifted - 2n * 10n ** BigInt(-(sA + 1) - e), e)]) {
      if (scaleOf(cand) > sA && checkConstraints(cand, b).length === 0) return cand;
    }
    return "infeasible";
  }
  const maxScale = latB.g - trailingZeros(latB.q);
  if (maxScale <= sA) return "infeasible";
  const kLo = latB.jLo === null ? null : ceilDiv(latB.jLo, latB.q);
  const kHi = latB.jHi === null ? null : floorDiv(latB.jHi, latB.q);
  for (const k of kByMagnitude(kLo, kHi)) {
    if (k === 0n) continue;
    const v = fromScaledInt(k * latB.q, -latB.g);
    if (scaleOf(v) > sA && checkConstraints(v, b).length === 0) return v;
  }
  return kLo !== null && kHi !== null ? "infeasible" : "undetermined";
}
