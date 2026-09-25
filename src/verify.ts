// Independent brute-force checker for small domains. Enumerates every point
// of the explicit finite domain and cross-checks the abstract engine's
// containment verdicts, counterexample validity and minimality, and the
// intersection / union / difference set algebra.

import {
  difference,
  evalPoint,
  intersect,
  union,
  type Cond,
  type MaybeValue,
  type Point,
} from './conditions.ts';
import { comparePoints, domainOrder, pointKey, type Domain } from './domain.ts';
import type { ContainmentResult } from './contain.ts';

const MAX_ENUMERATION = 200_000;

/** All points of the domain in lexicographic order (⊥ first per variable). */
export function enumeratePoints(domain: Domain): Point[] {
  const order = domainOrder(domain);
  const axes: MaybeValue[][] = order.map((v) => {
    const vd = domain[v];
    const concrete: MaybeValue[] = vd.kind === 'int'
      ? Array.from({ length: Math.max(0, vd.hi - vd.lo + 1) }, (_, i) => vd.lo + i)
      : [...vd.values];
    return vd.optional === true ? [null, ...concrete] : concrete;
  });
  const total = axes.reduce((acc, axis) => acc * axis.length, 1);
  if (total > MAX_ENUMERATION) throw new Error(`domain too large for brute force: ${total} points`);

  const points: Point[] = [];
  const current: Record<string, MaybeValue> = {};
  const walk = (depth: number): void => {
    if (depth === order.length) { points.push({ ...current }); return; }
    for (const value of axes[depth]) {
      current[order[depth]] = value;
      walk(depth + 1);
    }
  };
  walk(0);
  return points;
}

/** Points where `containee` is definitely true and `container` is not. */
export function bruteViolations(container: Cond, containee: Cond, domain: Domain): Point[] {
  return enumeratePoints(domain).filter(
    (p) => evalPoint(containee, p) === 'T' && evalPoint(container, p) !== 'T',
  );
}

export function bruteMinCounterexample(container: Cond, containee: Cond, domain: Domain): Point | null {
  const order = domainOrder(domain);
  const violations = bruteViolations(container, containee, domain);
  if (violations.length === 0) return null;
  let min = violations[0];
  for (const v of violations) if (comparePoints(domain, order, v, min) < 0) min = v;
  return min;
}

/**
 * Cross-check an engine result against brute force. Returns a list of
 * problems; an empty list means the engine agrees with exhaustive
 * enumeration and any counterexample is valid and minimal.
 */
export function checkContainment(
  container: Cond,
  containee: Cond,
  domain: Domain,
  result: ContainmentResult,
): string[] {
  const problems: string[] = [];
  const order = domainOrder(domain);
  const violations = bruteViolations(container, containee, domain);
  const min = violations.length === 0 ? null : bruteMinCounterexample(container, containee, domain);

  switch (result.status) {
    case 'contained':
      if (violations.length > 0) {
        problems.push(`engine says contained but brute force found ${violations.length} violation(s), e.g. ${pointKey(violations[0], order)}`);
      }
      break;
    case 'not-contained': {
      if (violations.length === 0) {
        problems.push('engine reports a counterexample but brute force found none');
        break;
      }
      const cx = result.counterexample;
      if (evalPoint(containee, cx) !== 'T' || evalPoint(container, cx) === 'T') {
        problems.push(`counterexample ${pointKey(cx, order)} is not a real violation`);
      }
      if (min !== null && pointKey(cx, order) !== pointKey(min, order)) {
        problems.push(`counterexample ${pointKey(cx, order)} is not minimal; expected ${pointKey(min, order)}`);
      }
      break;
    }
    case 'undecided':
      problems.push(`engine returned undecided (${result.reason}) on an enumerable domain`);
      break;
  }
  return problems;
}

/**
 * Verify the set algebra on T-sets: intersect = ∩, union = ∪, difference = ∖.
 */
export function checkSetOps(p: Cond, q: Cond, domain: Domain): string[] {
  const problems: string[] = [];
  const order = domainOrder(domain);
  const points = enumeratePoints(domain);
  const tSet = (c: Cond): Set<string> => {
    const s = new Set<string>();
    for (const pt of points) if (evalPoint(c, pt) === 'T') s.add(pointKey(pt, order));
    return s;
  };
  const tp = tSet(p);
  const tq = tSet(q);
  const expect = (label: string, actual: Set<string>, wanted: Set<string>): void => {
    if (actual.size !== wanted.size || [...actual].some((k) => !wanted.has(k))) {
      problems.push(`${label}: expected ${wanted.size} point(s), got ${actual.size}`);
    }
  };
  expect('intersect', tSet(intersect(p, q)), new Set([...tp].filter((k) => tq.has(k))));
  expect('union', tSet(union(p, q)), new Set([...tp, ...tq]));
  expect('difference', tSet(difference(p, q)), new Set([...tp].filter((k) => !tq.has(k))));
  return problems;
}
