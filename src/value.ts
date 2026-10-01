// 比较值模型：精确点值，或带“按来源标记的误差项”的区间证据。
// 关键规则：相同 source 的误差是相关的（相消），不同 source 的误差才独立（相加）。

import { add, cmp, isZero, mul, pow10, rational, sub, ZERO, ONE } from './rational.ts';
import type { Rational } from './rational.ts';
import { decimalToRational, divideDecimal } from './decimal.ts';

export interface ErrorTerm {
  readonly source: string;
  readonly radius: Rational; // >= 0
}

export interface Evidence {
  readonly center: Rational;
  readonly errors: readonly ErrorTerm[];
}

export type RefineRule =
  | { readonly type: 'geometric'; readonly factor: Rational } // 每步 radius *= factor，0 < factor < 1
  | { readonly type: 'decimal-expansion'; readonly num: bigint; readonly den: bigint }; // 逐位展开 num/den

export type CompareValue =
  | { readonly kind: 'exact'; readonly value: Rational }
  | { readonly kind: 'interval'; readonly center: Rational; readonly errors: readonly ErrorTerm[] }
  | {
      readonly kind: 'refinable';
      readonly center: Rational;
      readonly errors: readonly ErrorTerm[];
      readonly generation: number;
      readonly rule: RefineRule;
    };

export function exact(value: Rational | bigint): CompareValue {
  return { kind: 'exact', value: typeof value === 'bigint' ? rational(value) : value };
}

export function interval(center: Rational, radius: Rational, source = 'literal'): CompareValue {
  if (cmp(radius, ZERO) < 0) throw new RangeError('interval: radius must be >= 0');
  if (isZero(radius)) return exact(center);
  return { kind: 'interval', center, errors: [{ source, radius }] };
}

export function withErrors(center: Rational, errors: readonly ErrorTerm[]): CompareValue {
  for (const e of errors) {
    if (cmp(e.radius, ZERO) < 0) throw new RangeError('withErrors: radius must be >= 0');
  }
  const clean = errors.filter((e) => !isZero(e.radius));
  if (clean.length === 0) return exact(center);
  return { kind: 'interval', center, errors: clean };
}

function expansionEvidence(num: bigint, den: bigint, generation: number): Evidence {
  const dec = divideDecimal(num, den, generation, 'truncate');
  return {
    center: decimalToRational(dec),
    errors: [{ source: `truncation(${num}/${den})`, radius: rational(1n, pow10(generation)) }],
  };
}

// num/den 的十进制展开：第 g 代为中心=截断到 g 位小数、半径=10^(-g) 的区间。
export function decimalExpansion(num: bigint, den: bigint): CompareValue {
  if (den === 0n) throw new RangeError('decimalExpansion: denominator must not be zero');
  const n = den < 0n ? -num : num;
  const d = den < 0n ? -den : den;
  const ev = expansionEvidence(n, d, 0);
  return { kind: 'refinable', center: ev.center, errors: ev.errors, generation: 0, rule: { type: 'decimal-expansion', num: n, den: d } };
}

export function geometric(center: Rational, radius: Rational, source: string, factor: Rational): CompareValue {
  if (cmp(radius, ZERO) <= 0) throw new RangeError('geometric: radius must be > 0');
  if (cmp(factor, ZERO) <= 0 || cmp(factor, ONE) >= 0) {
    throw new RangeError('geometric: factor must be in (0, 1)');
  }
  return { kind: 'refinable', center, errors: [{ source, radius }], generation: 0, rule: { type: 'geometric', factor } };
}

export function evidenceOf(v: CompareValue): Evidence {
  if (v.kind === 'exact') return { center: v.value, errors: [] };
  return { center: v.center, errors: v.errors };
}

// 差值证据 a - b：同一 source 的半径相消（相关误差），不同 source 的半径相加（独立误差）。
export function subtractEvidence(a: Evidence, b: Evidence): Evidence {
  const bySource = new Map<string, Rational>();
  for (const e of a.errors) {
    bySource.set(e.source, add(bySource.get(e.source) ?? ZERO, e.radius));
  }
  for (const e of b.errors) {
    bySource.set(e.source, sub(bySource.get(e.source) ?? ZERO, e.radius));
  }
  const errors: ErrorTerm[] = [];
  for (const [source, net] of bySource) {
    const radius = cmp(net, ZERO) < 0 ? sub(ZERO, net) : net;
    if (!isZero(radius)) errors.push({ source, radius });
  }
  return { center: sub(a.center, b.center), errors };
}

export function boundsOf(e: Evidence): { lower: Rational; upper: Rational } {
  let total = ZERO;
  for (const t of e.errors) total = add(total, t.radius);
  return { lower: sub(e.center, total), upper: add(e.center, total) };
}

export function refineValue(v: CompareValue): CompareValue | null {
  if (v.kind !== 'refinable') return null;
  const generation = v.generation + 1;
  const rule = v.rule;
  if (rule.type === 'geometric') {
    return {
      ...v,
      generation,
      errors: v.errors.map((e) => ({ ...e, radius: mul(e.radius, rule.factor) })),
    };
  }
  const ev = expansionEvidence(rule.num, rule.den, generation);
  return { kind: 'refinable', center: ev.center, errors: ev.errors, generation, rule };
}
