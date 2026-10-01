import type { Rational } from './rational.ts';
import { add, cmp, min as rmin, mul, sub, toDisplayString, ZERO } from './rational.ts';
import { maybeRound } from './rounding.ts';
import type { Bracket, CompositeSpec, TierMode, TierRule } from './rules.ts';
import { bracketContains, bracketLabel, fixedOf, rateOf, validateComposite } from './rules.ts';

/** Per-segment contribution evidence. */
export interface SegmentTrace {
  readonly rule: string;
  readonly bracketIndex: number;
  readonly bracket: string; // human-readable domain, endpoints explicit
  readonly overlap: Rational | null; // progressive: portion of x inside the bracket; null for whole-mode / degenerate
  readonly raw: Rational; // rate * amount + fixed
  readonly rounded: Rational; // after per-segment rounding (identity when not configured)
  readonly clamped: Rational; // after bracket min/max
}

export interface RuleTrace {
  readonly name: string;
  readonly mode: TierMode;
  readonly segments: SegmentTrace[];
  readonly subtotal: Rational; // sum of clamped segments
  readonly discount: Rational; // multiplier actually used (1 when absent)
  readonly total: Rational; // subtotal * discount
}

export interface EvalTrace {
  readonly input: Rational;
  readonly rules: RuleTrace[];
  readonly sum: Rational; // sum of rule totals (pre global clamp, pre final rounding)
  readonly clampedSum: Rational; // after global min/max
  readonly output: Rational; // after final rounding (the function value f(x))
}

function clamp(v: Rational, lo: Rational | undefined, hi: Rational | undefined): Rational {
  if (lo !== undefined && cmp(v, lo) < 0) return lo;
  if (hi !== undefined && cmp(v, hi) > 0) return hi;
  return v;
}

interface RawContribution {
  readonly overlap: Rational | null;
  readonly raw: Rational;
}

/**
 * Raw contribution of one bracket at x, before rounding/clamps.
 * Progressive: rate * (portion of x inside the bracket) + fixed once the portion is positive.
 *   At x exactly equal to the bracket lower bound the portion is empty, so the contribution is 0
 *   (the fixed fee triggers only on strictly positive overlap).
 * Whole: rate * x + fixed when x lies in the bracket, else 0.
 */
export function bracketContribution(rule: TierRule, b: Bracket, x: Rational): RawContribution {
  const rate = rateOf(b);
  const fixed = fixedOf(b);
  if (rule.mode === 'whole') {
    if (!bracketContains(b, x)) return { overlap: null, raw: ZERO };
    return { overlap: null, raw: add(mul(rate, x), fixed) };
  }
  const l = b.lower.value;
  const u = b.upper.value;
  if (l !== null && cmp(x, l) <= 0) return { overlap: ZERO, raw: ZERO };
  if (l === null) {
    // validated: rate must be 0 here, so this is a pure fixed offset active for every x
    return { overlap: null, raw: fixed };
  }
  const top = u === null ? x : rmin(x, u);
  const overlap = sub(top, l);
  return { overlap, raw: add(mul(rate, overlap), fixed) };
}

function evaluateRule(rule: TierRule, x: Rational): RuleTrace {
  const segments: SegmentTrace[] = [];
  let subtotal = ZERO;
  for (let i = 0; i < rule.brackets.length; i++) {
    const b = rule.brackets[i];
    const l = b.lower.value;
    // A bracket is active only when x actually reaches it; min/max never pull up an inactive bracket.
    const active = rule.mode === 'whole' ? bracketContains(b, x) : l === null || cmp(x, l) > 0;
    if (!active) continue;
    const { overlap, raw } = bracketContribution(rule, b, x);
    const rounded = maybeRound(raw, rule.segmentRounding);
    const clamped = clamp(rounded, b.min, b.max);
    const show = rule.mode === 'whole' || cmp(raw, ZERO) !== 0 || (overlap !== null && cmp(overlap, ZERO) > 0);
    if (show) {
      segments.push({
        rule: rule.name,
        bracketIndex: i,
        bracket: bracketLabel(b),
        overlap,
        raw,
        rounded,
        clamped,
      });
    }
    subtotal = add(subtotal, clamped);
  }
  const discount = rule.discount ?? { n: 1n, d: 1n };
  return { name: rule.name, mode: rule.mode, segments, subtotal, discount, total: mul(subtotal, discount) };
}

/** Ground-truth evaluation of the composite function, with full contribution evidence. */
export function evaluate(spec: CompositeSpec, x: Rational): EvalTrace {
  validateComposite(spec);
  const rules = spec.rules.map((r) => evaluateRule(r, x));
  let sum = ZERO;
  for (const r of rules) sum = add(sum, r.total);
  const clampedSum = clamp(sum, spec.min, spec.max);
  const output = maybeRound(clampedSum, spec.finalRounding);
  return { input: x, rules, sum, clampedSum, output };
}

/** Renders the evidence trace as plain text (used by the demo and for debugging). */
export function formatTrace(trace: EvalTrace): string {
  const lines: string[] = [];
  lines.push(`f(${toDisplayString(trace.input)})`);
  for (const rule of trace.rules) {
    lines.push(`  规则 ${rule.name} [${rule.mode === 'progressive' ? '累进分段' : '整量落档'}]`);
    for (const s of rule.segments) {
      const parts = [`段 ${s.bracket}`];
      if (s.overlap !== null) parts.push(`覆盖量=${toDisplayString(s.overlap)}`);
      parts.push(`原始=${toDisplayString(s.raw)}`);
      if (cmp(s.raw, s.rounded) !== 0) parts.push(`段舍入=${toDisplayString(s.rounded)}`);
      if (cmp(s.rounded, s.clamped) !== 0) parts.push(`钳制=${toDisplayString(s.clamped)}`);
      lines.push(`    ${parts.join('  ')}`);
    }
    lines.push(
      `    小计=${toDisplayString(rule.subtotal)}  折扣=${toDisplayString(rule.discount)}  合计=${toDisplayString(rule.total)}`,
    );
  }
  lines.push(`  总和=${toDisplayString(trace.sum)}`);
  if (cmp(trace.sum, trace.clampedSum) !== 0) lines.push(`  全局钳制后=${toDisplayString(trace.clampedSum)}`);
  if (cmp(trace.clampedSum, trace.output) !== 0) lines.push(`  最终舍入后=${toDisplayString(trace.output)}`);
  lines.push(`  输出=${toDisplayString(trace.output)}`);
  return lines.join('\n');
}
