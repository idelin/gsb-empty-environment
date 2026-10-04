import { Rational } from './rational';

/** 闭区间 [lo, hi]，lo <= hi。 */
export interface Interval {
  readonly lo: Rational;
  readonly hi: Rational;
}

export function interval(lo: Rational | number, hi: Rational | number): Interval {
  const l = typeof lo === 'number' ? Rational.of(lo) : lo;
  const h = typeof hi === 'number' ? Rational.of(hi) : hi;
  return { lo: l, hi: h };
}

export function isValidInterval(iv: Interval): boolean {
  return iv.lo.cmp(iv.hi) <= 0;
}

/** inner ⊆ outer（允许端点相等：收紧不要求严格）。 */
export function isTighteningOf(outer: Interval, inner: Interval): boolean {
  return outer.lo.cmp(inner.lo) <= 0 && inner.hi.cmp(outer.hi) <= 0;
}

export function intervalContains(iv: Interval, value: Rational): boolean {
  return iv.lo.cmp(value) <= 0 && value.cmp(iv.hi) <= 0;
}

/**
 * 严格不相交才有序：a 整体在 b 之下返回 -1，之上返回 1，否则 0。
 * 注意端点相接（a.hi == b.lo）不算不相交——真实值仍可能相等。
 */
export function disjointOrder(a: Interval, b: Interval): -1 | 0 | 1 {
  if (a.hi.lt(b.lo)) return -1;
  if (a.lo.gt(b.hi)) return 1;
  return 0;
}

/** 区间宽度，仅用于调度（挑选下一个 refine 对象），绝不用于排序判定。 */
export function width(iv: Interval): Rational {
  return iv.hi.sub(iv.lo);
}

export function formatInterval(iv: Interval): string {
  return `[${iv.lo.toString()}, ${iv.hi.toString()}]`;
}
