import { Rat, ratMin, ratMax } from './rational.ts';
import { roundTo } from './rounding.ts';
import type { RoundingSpec } from './rounding.ts';

// A bracket is a half-open/closed interval with explicit endpoints.
// lower === null means -Infinity, upper === null means +Infinity.
export interface Bracket {
  lower: Rat | null;
  lowerClosed: boolean;
  upper: Rat | null;
  upperClosed: boolean;
  rate: Rat;
  offset?: Rat; // flat mode only: contribution = rate * x + offset
}

// progressive: each bracket taxes only the portion of x inside it (marginal).
// flat: the single bracket containing x applies its rate to the whole x.
export type LadderMode = 'progressive' | 'flat';

export type RoundingAnchor = 'segment' | 'rule-total';

export interface LadderRule {
  name: string;
  mode: LadderMode;
  brackets: Bracket[];
  rounding?: RoundingSpec & { at: RoundingAnchor };
  discount?: Rat; // multiplier applied after rounding, before min/max
  min?: Rat;      // floor on the rule output, applied after discount
  max?: Rat;      // cap on the rule output, applied after discount
}

export interface LadderConfig {
  rules: LadderRule[];
  finalRounding?: RoundingSpec; // applied once to the summed total
}

export interface CompiledLadder {
  rules: LadderRule[];
  finalRounding: RoundingSpec | null;
}

export interface SegmentContribution {
  bracketIndex: number;
  inputPortion: Rat;
  rate: Rat;
  offset: Rat;
  raw: Rat;
  rounded: Rat; // equals raw unless rounding.at === 'segment'
}

export interface RuleEvidence {
  name: string;
  mode: LadderMode;
  matchedBracket: number | null;
  segments: SegmentContribution[];
  subtotalBeforeRounding: Rat;
  subtotalAfterRounding: Rat;
  discount: Rat;
  afterDiscount: Rat;
  min: Rat | null;
  max: Rat | null;
  clamped: boolean;
  contribution: Rat;
}

export interface Evaluation {
  input: Rat;
  rules: RuleEvidence[];
  totalBeforeFinalRounding: Rat;
  finalRounding: RoundingSpec | null;
  total: Rat;
}

export function compileLadder(config: LadderConfig): CompiledLadder {
  if (config.rules.length === 0) throw new Error('compileLadder: need at least one rule');
  for (const rule of config.rules) validateRule(rule);
  if (config.finalRounding && config.finalRounding.quantum.lte(Rat.zero())) {
    throw new Error('compileLadder: finalRounding quantum must be positive');
  }
  return {
    rules: config.rules.map((r) => ({ ...r, brackets: r.brackets.map((b) => ({ ...b })) })),
    finalRounding: config.finalRounding ?? null,
  };
}

function validateRule(rule: LadderRule): void {
  if (rule.brackets.length === 0) throw new Error(`rule ${rule.name}: no brackets`);
  if (rule.min && rule.max && rule.min.gt(rule.max)) {
    throw new Error(`rule ${rule.name}: min > max`);
  }
  if (rule.discount && rule.discount.sign() < 0) {
    throw new Error(`rule ${rule.name}: discount must be >= 0`);
  }
  if (rule.rounding && rule.rounding.quantum.lte(Rat.zero())) {
    throw new Error(`rule ${rule.name}: rounding quantum must be positive`);
  }
  let prev: Bracket | null = null;
  for (const b of rule.brackets) {
    if (b.lower && b.upper && !b.lower.lt(b.upper)) {
      throw new Error(`rule ${rule.name}: empty bracket at ${b.lower}`);
    }
    if (rule.mode === 'progressive' && b.lower === null) {
      throw new Error(`rule ${rule.name}: progressive brackets need a finite lower bound`);
    }
    if (rule.mode === 'progressive' && b.offset !== undefined) {
      throw new Error(`rule ${rule.name}: offset is only meaningful in flat mode`);
    }
    if (prev) {
      if (prev.upper === null) throw new Error(`rule ${rule.name}: bracket after +Infinity`);
      if (b.lower === null) throw new Error(`rule ${rule.name}: brackets not sorted`);
      if (b.lower.lt(prev.upper)) {
        throw new Error(`rule ${rule.name}: overlapping brackets at ${b.lower}`);
      }
      // Touching brackets: exactly one side may be closed, so a threshold
      // value is never claimed (and charged) by two brackets at once.
      if (b.lower.eq(prev.upper) && b.lowerClosed && prev.upperClosed) {
        throw new Error(`rule ${rule.name}: threshold ${b.lower} closed on both sides (double count)`);
      }
    }
    prev = b;
  }
}

export function bracketContains(b: Bracket, x: Rat): boolean {
  const aboveLower =
    b.lower === null || x.gt(b.lower) || (x.eq(b.lower) && b.lowerClosed);
  const belowUpper =
    b.upper === null || x.lt(b.upper) || (x.eq(b.upper) && b.upperClosed);
  return aboveLower && belowUpper;
}

// Portion of x attributed to a progressive bracket. Zero-width at the
// boundary, so touching thresholds are never counted twice.
function progressivePortion(b: Bracket, x: Rat): Rat {
  const lower = b.lower as Rat; // validated finite for progressive
  if (x.lte(lower)) return Rat.zero();
  if (b.upper === null) return x.sub(lower);
  return ratMin(x, b.upper).sub(lower);
}

export function evaluate(ladder: CompiledLadder, x: Rat): Evaluation {
  const rules = ladder.rules.map((rule) => evaluateRule(rule, x));
  const totalBefore = rules.reduce((acc, r) => acc.add(r.contribution), Rat.zero());
  const total = ladder.finalRounding
    ? roundTo(totalBefore, ladder.finalRounding)
    : totalBefore;
  return {
    input: x,
    rules,
    totalBeforeFinalRounding: totalBefore,
    finalRounding: ladder.finalRounding,
    total,
  };
}

function evaluateRule(rule: LadderRule, x: Rat): RuleEvidence {
  const perSegment = rule.rounding?.at === 'segment';
  const segments: SegmentContribution[] = [];
  let matchedBracket: number | null = null;

  if (rule.mode === 'progressive') {
    rule.brackets.forEach((b, i) => {
      const portion = progressivePortion(b, x);
      if (portion.isZero()) return; // brackets above x contribute nothing
      const raw = portion.mul(b.rate);
      segments.push({
        bracketIndex: i,
        inputPortion: portion,
        rate: b.rate,
        offset: Rat.zero(),
        raw,
        rounded: perSegment ? roundTo(raw, rule.rounding!) : raw,
      });
    });
  } else {
    const idx = rule.brackets.findIndex((b) => bracketContains(b, x));
    if (idx >= 0) {
      matchedBracket = idx;
      const b = rule.brackets[idx];
      const offset = b.offset ?? Rat.zero();
      const raw = x.mul(b.rate).add(offset);
      segments.push({
        bracketIndex: idx,
        inputPortion: x,
        rate: b.rate,
        offset,
        raw,
        rounded: perSegment ? roundTo(raw, rule.rounding!) : raw,
      });
    }
  }

  const subtotalBeforeRounding = segments.reduce((a, s) => a.add(s.raw), Rat.zero());
  let subtotalAfterRounding: Rat;
  if (perSegment) {
    subtotalAfterRounding = segments.reduce((a, s) => a.add(s.rounded), Rat.zero());
  } else if (rule.rounding?.at === 'rule-total') {
    subtotalAfterRounding = roundTo(subtotalBeforeRounding, rule.rounding);
  } else {
    subtotalAfterRounding = subtotalBeforeRounding;
  }

  const discount = rule.discount ?? Rat.one();
  const afterDiscount = subtotalAfterRounding.mul(discount);
  let contribution = afterDiscount;
  if (rule.min !== undefined) contribution = ratMax(contribution, rule.min);
  if (rule.max !== undefined) contribution = ratMin(contribution, rule.max);

  return {
    name: rule.name,
    mode: rule.mode,
    matchedBracket,
    segments,
    subtotalBeforeRounding,
    subtotalAfterRounding,
    discount,
    afterDiscount,
    min: rule.min ?? null,
    max: rule.max ?? null,
    clamped: !contribution.eq(afterDiscount),
    contribution,
  };
}
