/**
 * Feasibility intersection: combines the decimal range with the multipleOf
 * and digit constraints and either produces a concrete feasible witness or
 * precise no-solution evidence (the exact integer intervals and steps that
 * were searched at each fraction-digit scale).
 *
 * Everything runs on scaled BigInts. For a fraction-digit scale f the value
 * is a * 10^-f; multipleOf m = M * 10^-sm translates into an exact integer
 * divisor D_f of a, computed by stripping factors of 2 and 5 from M:
 *   a * 10^(sm-f) / M in Z   <=>   D_f | a,
 *   D_f = M' * 2^max(0, p-e) * 5^max(0, q-e),  e = sm - f,
 * where M = M' * 2^p * 5^q with M' coprime to 10.
 */

import type { Decimal, DecimalOptions } from "./decimal.ts";
import {
  DEFAULT_MAX_SCALE,
  ZERO,
  addInteger,
  ceilDiv,
  compare,
  floorDiv,
  fractionDigits,
  fromScaledInt,
  lcmBigInt,
  pow10,
  signedCoeffAt,
  toPlainString,
  toScientificString,
  totalDigits,
} from "./decimal.ts";
import { compileSchema } from "./schema.ts";
import type { CompiledSchema, DecimalSchema } from "./schema.ts";

export interface ScaleProbe {
  /** Fraction-digit scale f that was searched. */
  fractionDigits: number;
  /** Integer interval for the coefficient at that scale, after inclusivity. */
  integerInterval: [string, string];
  /** Required divisor of the integer coefficient (multipleOf x 10^shift). */
  step: string;
  /** Closest-to-zero feasible coefficient at this scale, if any. */
  candidate: string | null;
  /** Why the candidate was rejected by maxTotalDigits, if it was. */
  digitReject: string | null;
}

export interface IntersectOk {
  feasible: true;
  /** Witness value in plain positional notation. */
  witness: string;
  /** Witness in normalized scientific notation. */
  canonical: string;
  totalDigits: number;
  fractionDigits: number;
}

export interface IntersectFail {
  feasible: false;
  reason: "empty-interval" | "no-multiple-in-range" | "total-digits-exceeded";
  /** Human-readable precise explanation of why no value can satisfy the schema. */
  summary: string;
  /** Per-scale search evidence backing the summary. */
  probes: ScaleProbe[];
}

export type IntersectResult = IntersectOk | IntersectFail;

function inBounds(d: Decimal, c: CompiledSchema): boolean {
  if (c.lo) {
    const cmp = compare(d, c.lo.value);
    if (cmp < 0 || (cmp === 0 && !c.lo.inclusive)) return false;
  }
  if (c.hi) {
    const cmp = compare(d, c.hi.value);
    if (cmp > 0 || (cmp === 0 && !c.hi.inclusive)) return false;
  }
  return true;
}

/**
 * Closest-to-zero multiple of `step` inside [lo, hi] (null = unbounded side)
 * whose normalized positional digit count is at most `cap` (when given).
 * A value with <= cap digits at scale S is c * 10^t with |c| <= 10^cap - 1,
 * so we search t = 0..S+cap and require divisibility by lcm(step, 10^t).
 */
function findCandidate(
  loI: bigint | null,
  hiI: bigint | null,
  step: bigint,
  cap: number | undefined,
  scale: number,
): bigint | null {
  const tryRange = (lo: bigint | null, hi: bigint | null, st: bigint): bigint | null => {
    if (lo !== null && hi !== null && lo > hi) return null;
    if (lo !== null && lo > 0n) {
      const cand = ceilDiv(lo, st) * st;
      return hi !== null && cand > hi ? null : cand;
    }
    if (hi !== null && hi < 0n) {
      const cand = floorDiv(hi, st) * st;
      return lo !== null && cand < lo ? null : cand;
    }
    return 0n;
  };
  if (cap === undefined) return tryRange(loI, hiI, step);
  const capLimit = pow10(cap) - 1n;
  for (let t = 0; t <= scale + cap; t += 1) {
    const tenT = pow10(t);
    const st = lcmBigInt(step, tenT);
    const bound = capLimit * tenT;
    const lo2 = loI !== null && loI > -bound ? loI : -bound;
    const hi2 = hiI !== null && hiI < bound ? hiI : bound;
    const cand = tryRange(lo2, hi2, st);
    if (cand !== null && cand !== 0n) return cand;
  }
  return null;
}

export function intersect(schema: DecimalSchema, options: DecimalOptions = {}): IntersectResult {
  const maxScale = options.maxScale ?? DEFAULT_MAX_SCALE;
  const c = compileSchema(schema, options);
  const probes: ScaleProbe[] = [];
  const ok = (d: Decimal): IntersectOk => ({
    feasible: true,
    witness: toPlainString(d),
    canonical: toScientificString(d),
    totalDigits: totalDigits(d),
    fractionDigits: fractionDigits(d),
  });
  const fail = (
    reason: IntersectFail["reason"],
    summary: string,
  ): IntersectFail => ({ feasible: false, reason, summary, probes });

  // 1. Empty interval detection.
  if (c.lo && c.hi) {
    const cmp = compare(c.lo.value, c.hi.value);
    if (cmp > 0 || (cmp === 0 && !(c.lo.inclusive && c.hi.inclusive))) {
      return fail(
        "empty-interval",
        `empty interval: lower bound ${c.lo.inclusive ? "[" : "("}${toPlainString(c.lo.value)} and ` +
          `upper bound ${toPlainString(c.hi.value)}${c.hi.inclusive ? "]" : ")"} share no decimal value`,
      );
    }
  }

  const cap = c.maxTotalDigits;
  const digitOk = (d: Decimal): boolean => cap === undefined || totalDigits(d) <= cap;

  // 2. Zero satisfies every multipleOf and needs a single digit.
  if (inBounds(ZERO, c) && digitOk(ZERO)) return ok(ZERO);

  // 3. Pure interval (no multipleOf, no digit caps): decimals are dense, so a
  //    witness always exists unless the interval is empty.
  if (!c.multipleOf && c.maxFractionDigits === undefined && cap === undefined) {
    if (c.lo && c.lo.inclusive) return ok(c.lo.value);
    if (c.hi && c.hi.inclusive) return ok(c.hi.value);
    if (c.lo && !c.hi) return ok(addInteger(c.lo.value, 1n));
    if (c.hi && !c.lo) return ok(addInteger(c.hi.value, -1n));
    const lo = c.lo!;
    const hi = c.hi!;
    for (let f = Math.max(lo.value.scale, hi.value.scale, 0); f <= maxScale; f += 1) {
      const loI = signedCoeffAt(lo.value, f) + (lo.inclusive ? 0n : 1n);
      const hiI = signedCoeffAt(hi.value, f) - (hi.inclusive ? 0n : 1n);
      if (loI <= hiI) return ok(fromScaledInt(loI, f));
    }
    return fail("empty-interval", "bounds are closer than the finest scale allowed by the budget");
  }

  // 4. Decompose multipleOf: m = M' * 2^p * 5^q * 10^-sm, M' coprime to 10.
  let mPrime = 1n;
  let e2 = 0n;
  let e5 = 0n;
  let sm = 0;
  if (c.multipleOf) {
    sm = c.multipleOf.scale;
    let rest = c.multipleOf.coeff;
    while (rest % 2n === 0n) {
      rest /= 2n;
      e2 += 1n;
    }
    while (rest % 5n === 0n) {
      rest /= 5n;
      e5 += 1n;
    }
    mPrime = rest;
  }
  const stepAt = (f: number): bigint => {
    if (!c.multipleOf) return 1n;
    const e = BigInt(sm - f);
    const n2 = e2 > e ? e2 - e : 0n;
    const n5 = e5 > e ? e5 - e : 0n;
    return mPrime * 2n ** n2 * 5n ** n5;
  };

  // 5. Bound the fraction-digit scales that need searching.
  let fLimit: number;
  if (c.maxFractionDigits !== undefined) {
    fLimit = c.multipleOf ? Math.min(c.maxFractionDigits, Math.max(0, sm)) : c.maxFractionDigits;
  } else if (c.multipleOf) {
    // Multiples of m normalize to at most sm fraction digits.
    fLimit = Math.max(0, sm);
  } else {
    // Only maxTotalDigits constrains the shape: a non-zero value with <= cap
    // digits is a * 10^-f with |a| <= 10^cap - 1, so f is bounded by the
    // distance of the interval from zero.
    const limit = pow10(cap!) - 1n;
    const ref =
      c.lo && c.lo.value.sign > 0
        ? c.lo.value
        : c.hi && c.hi.value.sign < 0
          ? ({ ...c.hi.value, sign: 1 } as Decimal)
          : null;
    if (ref === null) {
      fLimit = 0; // interval contains zero; already handled above
    } else {
      if (compare(ref, fromScaledInt(limit, 0)) > 0) {
        return fail(
          "total-digits-exceeded",
          `every value in the interval has |v| >= ${toPlainString(ref)} > 10^${cap} - 1, ` +
            `so it needs more than ${cap} total digits`,
        );
      }
      fLimit = 0;
      while (compare(ref, fromScaledInt(limit, fLimit + 1)) <= 0) fLimit += 1;
    }
  }

  // 6. Search each fraction-digit scale for a feasible coefficient.
  let sawDigitReject = false;
  for (let f = 0; f <= fLimit; f += 1) {
    const S = Math.max(f, c.lo?.value.scale ?? 0, c.hi?.value.scale ?? 0, 0);
    const shift = pow10(S - f);
    let loI = c.lo ? signedCoeffAt(c.lo.value, S) : null;
    let hiI = c.hi ? signedCoeffAt(c.hi.value, S) : null;
    if (loI !== null && !c.lo!.inclusive) loI += 1n;
    if (hiI !== null && !c.hi!.inclusive) hiI -= 1n;
    const step = stepAt(f) * shift;
    const probe: ScaleProbe = {
      fractionDigits: f,
      integerInterval: [loI === null ? "-inf" : loI.toString(), hiI === null ? "+inf" : hiI.toString()],
      step: step.toString(),
      candidate: null,
      digitReject: null,
    };
    probes.push(probe);
    if (loI !== null && hiI !== null && loI > hiI) continue;
    const found = findCandidate(loI, hiI, step, cap, S);
    if (found === null) continue;
    const value = fromScaledInt(found, S);
    probe.candidate = toPlainString(value);
    if (digitOk(value)) return ok(value);
    probe.digitReject = `${toPlainString(value)} needs ${totalDigits(value)} total digits > maxTotalDigits ${cap}`;
    sawDigitReject = true;
  }

  const reason: IntersectFail["reason"] = sawDigitReject ? "total-digits-exceeded" : "no-multiple-in-range";
  const summary =
    reason === "total-digits-exceeded"
      ? `no value in the interval satisfies maxTotalDigits ${cap} together with the remaining ` +
        `constraints; see probes for the digit-rejected candidates at each scale`
      : `the interval contains no ${c.multipleOf ? `multiple of ${toPlainString(c.multipleOf)}` : "value"} ` +
        `at any fraction-digit scale 0..${fLimit}` +
        `${c.maxFractionDigits !== undefined ? ` (maxFractionDigits ${c.maxFractionDigits})` : ""}; ` +
        `see probes for the exact integer intervals and steps searched`;
  return fail(reason, summary);
}
