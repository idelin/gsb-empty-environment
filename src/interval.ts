import type { Rational } from './rational.ts';
import { cmp, toDisplayString } from './rational.ts';

/** Endpoint of an interval; value === null means unbounded (-inf on lo, +inf on hi). */
export interface Endpoint {
  readonly value: Rational | null;
  readonly closed: boolean;
}
export interface Interval {
  lo: Endpoint;
  hi: Endpoint;
}

export const pointInterval = (v: Rational): Interval => ({
  lo: { value: v, closed: true },
  hi: { value: v, closed: true },
});

export const wholeLine = (): Interval => ({
  lo: { value: null, closed: false },
  hi: { value: null, closed: false },
});

export const emptyInterval = (): Interval => ({
  lo: { value: { n: 0n, d: 1n }, closed: false },
  hi: { value: { n: 0n, d: 1n }, closed: false },
});

export function isEmpty(iv: Interval): boolean {
  const { lo, hi } = iv;
  if (lo.value === null || hi.value === null) return false;
  const c = cmp(lo.value, hi.value);
  if (c > 0) return true;
  if (c === 0 && !(lo.closed && hi.closed)) return true;
  return false;
}

export function contains(iv: Interval, v: Rational): boolean {
  if (iv.lo.value !== null) {
    const c = cmp(v, iv.lo.value);
    if (c < 0 || (c === 0 && !iv.lo.closed)) return false;
  }
  if (iv.hi.value !== null) {
    const c = cmp(v, iv.hi.value);
    if (c > 0 || (c === 0 && !iv.hi.closed)) return false;
  }
  return true;
}

function maxLo(a: Endpoint, b: Endpoint): Endpoint {
  if (a.value === null) return b;
  if (b.value === null) return a;
  const c = cmp(a.value, b.value);
  if (c > 0) return a;
  if (c < 0) return b;
  return { value: a.value, closed: a.closed && b.closed };
}
function minHi(a: Endpoint, b: Endpoint): Endpoint {
  if (a.value === null) return b;
  if (b.value === null) return a;
  const c = cmp(a.value, b.value);
  if (c < 0) return a;
  if (c > 0) return b;
  return { value: a.value, closed: a.closed && b.closed };
}

export function intersect(a: Interval, b: Interval): Interval {
  return { lo: maxLo(a.lo, b.lo), hi: minHi(a.hi, b.hi) };
}

export function intervalToString(iv: Interval): string {
  if (isEmpty(iv)) return '∅';
  const loS = iv.lo.value === null ? '-∞' : toDisplayString(iv.lo.value);
  const hiS = iv.hi.value === null ? '+∞' : toDisplayString(iv.hi.value);
  if (iv.lo.value !== null && iv.hi.value !== null && cmp(iv.lo.value, iv.hi.value) === 0 && iv.lo.closed && iv.hi.closed) {
    return `[${loS}]`;
  }
  return `${iv.lo.closed ? '[' : '('}${loS}, ${hiS}${iv.hi.closed ? ']' : ')'}`;
}

/** Sorts ascending and merges intervals whose union is contiguous (shared endpoint included by at least one side). */
export function mergeIntervals(list: Interval[]): Interval[] {
  const items = list.filter((iv) => !isEmpty(iv));
  items.sort((a, b) => {
    if (a.lo.value === null) return b.lo.value === null ? 0 : -1;
    if (b.lo.value === null) return 1;
    const c = cmp(a.lo.value, b.lo.value);
    if (c !== 0) return c;
    if (a.lo.closed !== b.lo.closed) return a.lo.closed ? -1 : 1;
    return 0;
  });
  const out: Interval[] = [];
  for (const iv of items) {
    const last = out[out.length - 1];
    if (!last) {
      out.push({ lo: { ...iv.lo }, hi: { ...iv.hi } });
      continue;
    }
    if (last.hi.value === null) continue; // already stretches to +inf
    if (iv.lo.value === null) continue; // cannot happen after the first sorted item
    const c = cmp(iv.lo.value, last.hi.value);
    const touches = c < 0 || (c === 0 && (last.hi.closed || iv.lo.closed));
    if (!touches) {
      out.push({ lo: { ...iv.lo }, hi: { ...iv.hi } });
      continue;
    }
    if (iv.hi.value === null) {
      last.hi = { value: null, closed: false };
    } else {
      const c2 = cmp(iv.hi.value, last.hi.value);
      if (c2 > 0) last.hi = { ...iv.hi };
      else if (c2 === 0) last.hi = { value: last.hi.value, closed: last.hi.closed || iv.hi.closed };
    }
  }
  return out;
}
