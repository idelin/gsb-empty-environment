/**
 * Minimal destructive counterexample: the simplest decimal literal that the
 * schema rejects. "Simplest" is ordered by (totalDigits, |value|, text).
 * Candidates are constructed directly from each constraint (e.g. m/2 for
 * multipleOf, 10^T for maxTotalDigits) and then verified through the real
 * evaluator, so a reported counterexample is guaranteed to fail validation.
 */

import type { Decimal, DecimalOptions } from "./decimal.ts";
import {
  ZERO,
  addInteger,
  compareAbs,
  fromScaledInt,
  toPlainString,
  toScientificString,
  totalDigits,
} from "./decimal.ts";
import { compileSchema } from "./schema.ts";
import type { CompiledSchema, DecimalSchema } from "./schema.ts";
import { evaluate } from "./validate.ts";

export interface CounterexampleOk {
  found: true;
  /** Counterexample in plain positional notation. */
  value: string;
  canonical: string;
  /** Keywords (rule paths) the value violates. */
  violates: string[];
  totalDigits: number;
}

export interface CounterexampleNone {
  found: false;
  reason: string;
}

export type CounterexampleResult = CounterexampleOk | CounterexampleNone;

function constraintCandidates(c: CompiledSchema): Decimal[] {
  const out: Decimal[] = [ZERO];
  if (c.lo) {
    if (!c.lo.inclusive) out.push(c.lo.value); // equal to an exclusive bound violates it
    out.push(addInteger(c.lo.value, -1n));
  }
  if (c.hi) {
    if (!c.hi.inclusive) out.push(c.hi.value);
    out.push(addInteger(c.hi.value, 1n));
  }
  if (c.multipleOf) {
    // m/2 is never an integer multiple of m.
    out.push(fromScaledInt(c.multipleOf.coeff * 5n, c.multipleOf.scale + 1));
  }
  if (c.maxTotalDigits !== undefined) {
    // 10^T has exactly T+1 positional digits.
    out.push(fromScaledInt(1n, -c.maxTotalDigits));
  }
  if (c.maxFractionDigits !== undefined) {
    // 10^-(F+1) has exactly F+1 fraction digits.
    out.push(fromScaledInt(1n, c.maxFractionDigits + 1));
  }
  return out;
}

export function counterexample(schema: DecimalSchema, options: DecimalOptions = {}): CounterexampleResult {
  const compiled = compileSchema(schema, options);
  const constrained =
    compiled.lo !== undefined ||
    compiled.hi !== undefined ||
    compiled.multipleOf !== undefined ||
    compiled.maxTotalDigits !== undefined ||
    compiled.maxFractionDigits !== undefined;
  if (!constrained) {
    return { found: false, reason: "schema imposes no constraints; every decimal literal is accepted" };
  }
  let best: Decimal | null = null;
  let bestErrors: string[] = [];
  for (const candidate of constraintCandidates(compiled)) {
    const errors = evaluate(candidate, compiled);
    if (errors.length === 0) continue;
    if (
      best === null ||
      totalDigits(candidate) < totalDigits(best) ||
      (totalDigits(candidate) === totalDigits(best) && compareAbs(candidate, best) < 0) ||
      (totalDigits(candidate) === totalDigits(best) &&
        compareAbs(candidate, best) === 0 &&
        toPlainString(candidate) < toPlainString(best))
    ) {
      best = candidate;
      bestErrors = errors.map((e) => e.rulePath);
    }
  }
  if (best === null) {
    // Unreachable for the implemented subset, but kept as a safety net.
    return { found: false, reason: "no counterexample found within the constructed candidate set" };
  }
  return {
    found: true,
    value: toPlainString(best),
    canonical: toScientificString(best),
    violates: bestErrors,
    totalDigits: totalDigits(best),
  };
}
