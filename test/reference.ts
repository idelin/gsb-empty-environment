/**
 * Independent brute-force reference used to cross-check the engine.
 * Shares no code with src/: own fraction arithmetic (plain numbers, exact for
 * the small test magnitudes), own condition evaluation, own enumeration.
 */
import type { Condition } from '../src/condition.ts';
import type { PolicySpec } from '../src/policy.ts';

export interface RefFrac {
  n: number;
  d: number;
}

function gcd(a: number, b: number): number {
  a = Math.abs(a);
  b = Math.abs(b);
  while (b) [a, b] = [b, a % b];
  return a || 1;
}

export function frac(n: number, d = 1): RefFrac {
  if (d < 0) [n, d] = [-n, -d];
  const g = gcd(n, d);
  return { n: n / g, d: d / g };
}

export function fadd(a: RefFrac, b: RefFrac): RefFrac {
  return frac(a.n * b.d + b.n * a.d, a.d * b.d);
}

export function fcmp(a: RefFrac, b: RefFrac): number {
  return Math.sign(a.n * b.d - b.n * a.d);
}

/** Converts a policy weight literal to a fraction (numbers stay exact here). */
export function toFrac(w: unknown): RefFrac {
  if (typeof w === 'number') {
    if (Number.isInteger(w)) return frac(w);
    const s = w.toString();
    const dec = s.includes('.') ? s.split('.')[1].length : 0;
    return frac(Math.round(w * 10 ** dec), 10 ** dec);
  }
  if (typeof w === 'string') {
    const [n, d] = w.split('/').map(Number);
    return frac(n, d ?? 1);
  }
  if (w && typeof w === 'object' && 'num' in (w as any)) {
    const o = w as { num: number; den?: number };
    return frac(Number(o.num), Number(o.den ?? 1));
  }
  return frac(0);
}

export function refEval(cond: Condition, attrs: Record<string, number>): boolean {
  switch (cond.kind) {
    case 'const':
      return cond.value;
    case 'cmp': {
      const a = attrs[cond.attr];
      switch (cond.op) {
        case 'eq': return a === cond.value;
        case 'ne': return a !== cond.value;
        case 'lt': return a < cond.value;
        case 'le': return a <= cond.value;
        case 'gt': return a > cond.value;
        case 'ge': return a >= cond.value;
      }
      break;
    }
    case 'in':
      return cond.set.includes(attrs[cond.attr]);
    case 'and':
      return cond.args.every((c) => refEval(c, attrs));
    case 'or':
      return cond.args.some((c) => refEval(c, attrs));
    case 'not':
      return !refEval(cond.arg, attrs);
  }
}

export interface RefResult {
  /** Sorted unique outcome keys: grade names and/or 'REJECT'. */
  outcomes: string[];
  minScore: RefFrac | null;
  maxScore: RefFrac | null;
  /** Grade per distinct score, for midpoint-fallacy checks. */
  scoreToGrade: Map<string, string>;
}

/** Deduplicates rules by id the same way the spec demands (first wins). */
export function refDedupeRules(spec: PolicySpec): PolicySpec['rules'] {
  const seen = new Set<string>();
  const out: PolicySpec['rules'][number][] = [];
  for (const r of spec.rules) {
    if (seen.has(r.id)) continue;
    seen.add(r.id);
    out.push(r);
  }
  return out;
}

export function refGradeOf(spec: PolicySpec, score: RefFrac): string {
  let grade = spec.bands[0].grade;
  for (const band of spec.bands) {
    if (band.gte === null || fcmp(score, toFrac(band.gte)) >= 0) grade = band.grade;
    else break;
  }
  return grade;
}

export function refEvaluate(spec: PolicySpec, known: Record<string, number>): RefResult {
  const rules = refDedupeRules(spec);
  const unknownNames = Object.keys(spec.attributes).filter((n) => !(n in known)).sort();
  const domains = unknownNames.map((n) => spec.attributes[n]);

  const outcomes = new Set<string>();
  const scoreToGrade = new Map<string, string>();
  let minScore: RefFrac | null = null;
  let maxScore: RefFrac | null = null;

  const assignment: Record<string, number> = { ...known };
  const visit = (depth: number): void => {
    if (depth === unknownNames.length) {
      let rejected = false;
      let score = frac(0);
      for (const rule of rules) {
        if (!refEval(rule.when as Condition, assignment)) continue;
        if (rule.hardReject) rejected = true;
        else score = fadd(score, toFrac(rule.weight ?? 0));
      }
      if (rejected) {
        outcomes.add('REJECT');
      } else {
        const grade = refGradeOf(spec, score);
        outcomes.add(grade);
        scoreToGrade.set(`${score.n}/${score.d}`, grade);
        if (minScore === null || fcmp(score, minScore) < 0) minScore = score;
        if (maxScore === null || fcmp(score, maxScore) > 0) maxScore = score;
      }
      return;
    }
    for (const v of domains[depth]) {
      assignment[unknownNames[depth]] = v;
      visit(depth + 1);
    }
    delete assignment[unknownNames[depth]];
  };
  visit(0);

  const bandOrder = new Map(spec.bands.map((b, i) => [b.grade, i]));
  const sorted = [...outcomes].sort((a, b) => {
    if (a === 'REJECT') return 1;
    if (b === 'REJECT') return -1;
    return bandOrder.get(a)! - bandOrder.get(b)!;
  });
  return { outcomes: sorted, minScore, maxScore, scoreToGrade };
}
