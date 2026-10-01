import type { Rational } from './rational.ts';
import { cmp, eq, isZero, toDisplayString, ZERO } from './rational.ts';
import type { RoundingSpec } from './rounding.ts';

/** Interval bound; value === null means unbounded (-inf as lower, +inf as upper). Open/closed is explicit. */
export interface Bound {
  readonly value: Rational | null;
  readonly closed: boolean;
}

export interface Bracket {
  readonly lower: Bound;
  readonly upper: Bound;
  readonly rate?: Rational; // per-unit rate, default 0
  readonly fixed?: Rational; // fixed amount, default 0
  readonly min?: Rational; // per-segment minimum contribution (after segment rounding)
  readonly max?: Rational; // per-segment maximum contribution (after segment rounding)
}

/**
 * 'progressive' (累进分段): each bracket taxes only the portion of x inside it.
 * 'whole' (整量落档): the single bracket containing x applies to all of x.
 */
export type TierMode = 'progressive' | 'whole';

export interface TierRule {
  readonly name: string;
  readonly mode: TierMode;
  readonly brackets: readonly Bracket[];
  readonly segmentRounding?: RoundingSpec; // applied to each bracket contribution, independently of final rounding
  readonly discount?: Rational; // multiplier on the rule subtotal, default 1
}

export interface CompositeSpec {
  readonly rules: readonly TierRule[];
  readonly min?: Rational; // global floor on the summed total
  readonly max?: Rational; // global cap on the summed total
  readonly finalRounding?: RoundingSpec; // applied once to the final total, never swapped with segment rounding
}

export function rateOf(b: Bracket): Rational {
  return b.rate ?? ZERO;
}
export function fixedOf(b: Bracket): Rational {
  return b.fixed ?? ZERO;
}

export function bracketContains(b: Bracket, x: Rational): boolean {
  if (b.lower.value !== null) {
    const c = cmp(x, b.lower.value);
    if (c < 0 || (c === 0 && !b.lower.closed)) return false;
  }
  if (b.upper.value !== null) {
    const c = cmp(x, b.upper.value);
    if (c > 0 || (c === 0 && !b.upper.closed)) return false;
  }
  return true;
}

export function bracketLabel(b: Bracket): string {
  const lo = b.lower.value === null ? '-∞' : toDisplayString(b.lower.value);
  const hi = b.upper.value === null ? '+∞' : toDisplayString(b.upper.value);
  return `${b.lower.closed ? '[' : '('}${lo}, ${hi}${b.upper.closed ? ']' : ')'}`;
}

/**
 * Structural validation. Brackets of a rule must partition the whole real line:
 * sorted, first lower = -inf, last upper = +inf, and at every shared boundary
 * exactly one side is closed (no gap, no double-count at the threshold).
 */
export function validateComposite(spec: CompositeSpec): void {
  if (spec.rules.length === 0) throw new Error('composite: at least one rule is required');
  if (spec.min !== undefined && spec.max !== undefined && cmp(spec.min, spec.max) > 0) {
    throw new Error('composite: min must not exceed max');
  }
  if (spec.finalRounding !== undefined && spec.finalRounding.quantum.n <= 0n) {
    throw new Error('composite: finalRounding quantum must be positive');
  }
  for (const rule of spec.rules) {
    if (rule.brackets.length === 0) throw new Error(`rule ${rule.name}: needs at least one bracket`);
    if (rule.segmentRounding !== undefined && rule.segmentRounding.quantum.n <= 0n) {
      throw new Error(`rule ${rule.name}: segmentRounding quantum must be positive`);
    }
    const first = rule.brackets[0];
    const last = rule.brackets[rule.brackets.length - 1];
    if (first.lower.value !== null) throw new Error(`rule ${rule.name}: first bracket must start at -inf`);
    if (last.upper.value !== null) throw new Error(`rule ${rule.name}: last bracket must end at +inf`);
    for (let i = 0; i < rule.brackets.length; i++) {
      const b = rule.brackets[i];
      if (b.lower.value !== null && b.upper.value !== null && cmp(b.lower.value, b.upper.value) >= 0) {
        throw new Error(`rule ${rule.name}: bracket ${i} has zero or negative width`);
      }
      if (b.min !== undefined && b.max !== undefined && cmp(b.min, b.max) > 0) {
        throw new Error(`rule ${rule.name}: bracket ${i} min exceeds max`);
      }
      if (rule.mode === 'progressive' && b.lower.value === null && !isZero(rateOf(b))) {
        throw new Error(`rule ${rule.name}: progressive bracket with lower=-inf must have rate 0 (overlap would be infinite)`);
      }
      if (i > 0) {
        const prev = rule.brackets[i - 1];
        const pu = prev.upper;
        const nl = b.lower;
        if (pu.value === null || nl.value === null || !eq(pu.value, nl.value)) {
          throw new Error(`rule ${rule.name}: gap between brackets ${i - 1} and ${i}`);
        }
        if (pu.closed === nl.closed) {
          throw new Error(
            `rule ${rule.name}: boundary ${toDisplayString(pu.value)} is ${
              pu.closed ? 'closed on both sides (threshold double-counted)' : 'open on both sides (threshold uncovered)'
            }`,
          );
        }
      }
    }
  }
}
