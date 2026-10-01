import { Rat } from './rational.ts';
import { roundTo } from './rounding.ts';
import type { RoundingSpec } from './rounding.ts';
import { bracketContains, evaluate } from './ladder.ts';
import type { Bracket, CompiledLadder, LadderRule } from './ladder.ts';

export interface Interval {
  lower: Rat;
  lowerClosed: boolean;
  upper: Rat;
  upperClosed: boolean;
}

export interface SearchWindow {
  from: Rat;
  to: Rat;
}

export function intervalContains(iv: Interval, x: Rat): boolean {
  const above = x.gt(iv.lower) || (x.eq(iv.lower) && iv.lowerClosed);
  const below = x.lt(iv.upper) || (x.eq(iv.upper) && iv.upperClosed);
  return above && below;
}

// Points where the rounded value of a quantity ranging over [lo, hi] can jump.
function roundingThresholds(spec: RoundingSpec, lo: Rat, hi: Rat): Rat[] {
  const q = spec.quantum;
  const out: Rat[] = [];
  const kLo = lo.div(q).floor() - 1n;
  const kHi = hi.div(q).ceil() + 1n;
  for (let k = kLo; k <= kHi; k++) {
    const t =
      spec.mode === 'half-up' || spec.mode === 'half-down' || spec.mode === 'half-even'
        ? q.mul(Rat.of(2n * k + 1n, 2n))
        : q.mul(Rat.of(k));
    if (t.gte(lo) && t.lte(hi)) out.push(t);
  }
  return out;
}

interface Linear {
  slope: Rat;
  intercept: Rat;
}

function linearAt(l: Linear, x: Rat): Rat {
  return l.slope.mul(x).add(l.intercept);
}

// Linear form of a rule's subtotal (before rule-total rounding, discount and
// clamps) on the open interval (a, b). The caller guarantees the interval
// contains no bracket boundary and no per-segment rounding step.
function ruleSubtotalLinear(rule: LadderRule, a: Rat, b: Rat): Linear {
  const mid = a.add(b).div(Rat.of(2n));
  const perSegment = rule.rounding?.at === 'segment';
  let slope = Rat.zero();
  let intercept = Rat.zero();

  if (rule.mode === 'progressive') {
    for (const br of rule.brackets) {
      const lower = br.lower as Rat;
      if (!b.gt(lower)) continue; // interval at or below this bracket
      if (br.upper !== null && !a.lt(br.upper)) {
        const c = br.upper.sub(lower).mul(br.rate);
        intercept = intercept.add(perSegment ? constRound(rule, c) : c);
        continue;
      }
      if (perSegment) {
        intercept = intercept.add(constRound(rule, mid.sub(lower).mul(br.rate)));
      } else {
        slope = slope.add(br.rate);
        intercept = intercept.sub(br.rate.mul(lower));
      }
    }
  } else {
    const br = rule.brackets.find((cand) => bracketContains(cand, mid));
    if (br) {
      const off = br.offset ?? Rat.zero();
      if (perSegment) {
        intercept = constRound(rule, mid.mul(br.rate).add(off));
      } else {
        slope = br.rate;
        intercept = off;
      }
    }
  }
  return { slope, intercept };
}

function constRound(rule: LadderRule, v: Rat): Rat {
  return roundTo(v, rule.rounding!);
}

function bracketWindowIntersection(
  br: Bracket,
  win: SearchWindow,
): { lo: Rat; hi: Rat } | null {
  const lo = br.lower === null || br.lower.lt(win.from) ? win.from : br.lower;
  const hi = br.upper === null || br.upper.gt(win.to) ? win.to : br.upper;
  return lo.lt(hi) ? { lo, hi } : null;
}

function collectInitialBreakpoints(ladder: CompiledLadder, win: SearchWindow): Rat[] {
  const pts: Rat[] = [win.from, win.to];
  for (const rule of ladder.rules) {
    for (const br of rule.brackets) {
      if (br.lower && br.lower.gt(win.from) && br.lower.lt(win.to)) pts.push(br.lower);
      if (br.upper && br.upper.gt(win.from) && br.upper.lt(win.to)) pts.push(br.upper);
    }
    if (rule.rounding?.at !== 'segment') continue;
    for (const br of rule.brackets) {
      const span = bracketWindowIntersection(br, win);
      if (!span || br.rate.isZero()) continue;
      if (rule.mode === 'progressive') {
        const lower = br.lower as Rat;
        const rawLo = br.rate.mul(span.lo.sub(lower));
        const rawHi = br.rate.mul(span.hi.sub(lower));
        for (const t of roundingThresholds(rule.rounding, minR(rawLo, rawHi), maxR(rawLo, rawHi))) {
          const x = lower.add(t.div(br.rate));
          if (x.gt(win.from) && x.lt(win.to)) pts.push(x);
        }
      } else {
        const off = br.offset ?? Rat.zero();
        const rawLo = br.rate.mul(span.lo).add(off);
        const rawHi = br.rate.mul(span.hi).add(off);
        for (const t of roundingThresholds(rule.rounding, minR(rawLo, rawHi), maxR(rawLo, rawHi))) {
          const x = t.sub(off).div(br.rate);
          if (x.gt(win.from) && x.lt(win.to)) pts.push(x);
        }
      }
    }
  }
  return pts;
}

function minR(a: Rat, b: Rat): Rat { return a.lte(b) ? a : b; }
function maxR(a: Rat, b: Rat): Rat { return a.gte(b) ? a : b; }

// Linear form of the composed total (before final rounding) on the open
// piece (a, b), valid once the piece contains no rule-level steps or kinks.
function totalLinearForm(ladder: CompiledLadder, a: Rat, b: Rat): Linear {
  const mid = a.add(b).div(Rat.of(2n));
  let slope = Rat.zero();
  let intercept = Rat.zero();

  for (const rule of ladder.rules) {
    const L = ruleSubtotalLinear(rule, a, b);
    const discount = rule.discount ?? Rat.one();

    if (rule.rounding?.at === 'rule-total') {
      // On a fully refined piece the rounded subtotal is constant.
      intercept = intercept.add(ruleOutputAt(rule, mid));
      continue;
    }

    const outMid = ruleOutputAt(rule, mid);
    const rawMid = linearAt(L, mid).mul(discount);
    const clamped =
      (rule.min !== undefined && rawMid.lt(rule.min)) ||
      (rule.max !== undefined && rawMid.gt(rule.max));
    if (clamped) {
      intercept = intercept.add(outMid);
    } else {
      slope = slope.add(L.slope.mul(discount));
      intercept = intercept.add(L.intercept.mul(discount));
    }
  }
  return { slope, intercept };
}

// Extra split points inside open piece (a, b): rule-total rounding steps,
// discount/min/max kinks, and final-rounding steps of the composed total.
function collectRefinementPoints(ladder: CompiledLadder, a: Rat, b: Rat): Rat[] {
  const pts: Rat[] = [];

  for (const rule of ladder.rules) {
    const L = ruleSubtotalLinear(rule, a, b);
    const discount = rule.discount ?? Rat.one();

    if (rule.rounding?.at === 'rule-total') {
      if (!L.slope.isZero()) {
        const sLo = linearAt(L, a);
        const sHi = linearAt(L, b);
        for (const t of roundingThresholds(rule.rounding, minR(sLo, sHi), maxR(sLo, sHi))) {
          const x = t.sub(L.intercept).div(L.slope);
          if (x.gt(a) && x.lt(b)) pts.push(x);
        }
      }
      // After this piece is split at every rounding step, the rounded
      // subtotal is constant inside, so discount and clamps add no kinks.
      continue;
    }

    if (!L.slope.isZero() && !discount.isZero()) {
      for (const bound of [rule.min, rule.max]) {
        if (bound === undefined) continue;
        const x = bound.div(discount).sub(L.intercept).div(L.slope);
        if (x.gt(a) && x.lt(b)) pts.push(x);
      }
    }
  }

  const T = totalLinearForm(ladder, a, b);
  if (ladder.finalRounding && !T.slope.isZero()) {
    const tLo = linearAt(T, a);
    const tHi = linearAt(T, b);
    for (const t of roundingThresholds(ladder.finalRounding, minR(tLo, tHi), maxR(tLo, tHi))) {
      const x = t.sub(T.intercept).div(T.slope);
      if (x.gt(a) && x.lt(b)) pts.push(x);
    }
  }
  return pts;
}

function ruleOutputAt(rule: LadderRule, x: Rat): Rat {
  return evaluate({ rules: [rule], finalRounding: null }, x).total;
}

function uniqueSorted(pts: Rat[]): Rat[] {
  const map = new Map<string, Rat>();
  for (const p of pts) map.set(p.toString(), p);
  return [...map.values()].sort((u, v) => u.cmp(v));
}

// All input intervals inside `win` whose composed output equals `target`.
// Exact: works on rationals only, so plateaus from rounding or flat brackets
// come back as genuine intervals, and touching pieces are merged with correct
// open/closed endpoints.
export function invert(ladder: CompiledLadder, target: Rat, win: SearchWindow): Interval[] {
  if (!win.from.lt(win.to)) throw new Error('invert: window must satisfy from < to');

  let bps = collectInitialBreakpoints(ladder, win);
  for (let iter = 0; iter < 8; iter++) {
    const sorted = uniqueSorted(bps);
    const extra: Rat[] = [];
    for (let i = 0; i + 1 < sorted.length; i++) {
      extra.push(...collectRefinementPoints(ladder, sorted[i], sorted[i + 1]));
    }
    if (extra.length === 0) break;
    if (iter === 7) throw new Error('invert: refinement did not converge');
    bps = sorted.concat(extra);
  }

  let sorted = uniqueSorted(bps);
  if (!ladder.finalRounding) {
    // Without a final rounding the total is linear (not stepped) on each
    // atomic piece, so a target can be hit at a single interior point.
    const extra: Rat[] = [];
    for (let i = 0; i + 1 < sorted.length; i++) {
      const T = totalLinearForm(ladder, sorted[i], sorted[i + 1]);
      if (T.slope.isZero()) continue;
      const x = target.sub(T.intercept).div(T.slope);
      if (x.gt(sorted[i]) && x.lt(sorted[i + 1])) extra.push(x);
    }
    if (extra.length > 0) sorted = uniqueSorted(sorted.concat(extra));
  }

  type Piece = { lo: Rat; loClosed: boolean; hi: Rat; hiClosed: boolean; match: boolean };
  const pieces: Piece[] = [];
  for (let i = 0; i < sorted.length; i++) {
    const p = sorted[i];
    pieces.push({
      lo: p, loClosed: true, hi: p, hiClosed: true,
      match: evaluate(ladder, p).total.eq(target),
    });
    if (i + 1 < sorted.length) {
      const q = sorted[i + 1];
      const mid = p.add(q).div(Rat.of(2n));
      pieces.push({
        lo: p, loClosed: false, hi: q, hiClosed: false,
        match: evaluate(ladder, mid).total.eq(target),
      });
    }
  }

  const out: Interval[] = [];
  let cur: Interval | null = null;
  for (const piece of pieces) {
    if (piece.match) {
      if (!cur) {
        cur = { lower: piece.lo, lowerClosed: piece.loClosed, upper: piece.hi, upperClosed: piece.hiClosed };
      } else {
        cur.upper = piece.hi;
        cur.upperClosed = piece.hiClosed;
      }
    } else if (cur) {
      out.push(cur);
      cur = null;
    }
  }
  if (cur) out.push(cur);
  return out;
}
