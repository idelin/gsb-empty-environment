import { Rational } from './rational.ts';
import type { Measurement } from './measurement.ts';
import { intervalOf } from './measurement.ts';
import type { LinearConstraint } from './constraint.ts';
import { satisfies } from './constraint.ts';
import type { Assignment } from './classify.ts';

/**
 * Small-domain enumeration: subdivide each (bounded) measurement interval
 * into a rational grid and check every grid point exactly. Used to verify
 * boundary behavior of the classifier on small domains — never as the
 * primary decision procedure.
 */

export interface EnumerationResult {
  readonly totalPoints: number;
  readonly allSatisfied: number;
  readonly perConstraint: ReadonlyMap<string, { pass: number; fail: number }>;
  /** First few points where the full constraint set fails (exact rationals). */
  readonly violatingSamples: readonly Assignment[];
}

export function gridPoints(
  measurements: readonly Measurement[],
  divisions: number,
  maxPoints = 1_000_000,
): Assignment[] {
  if (!Number.isSafeInteger(divisions) || divisions < 1) {
    throw new Error('gridPoints: divisions must be a positive integer');
  }
  const axes: { id: string; values: Rational[] }[] = [];
  let total = 1;
  for (const m of measurements) {
    const iv = intervalOf(m);
    if (iv.lo.kind === 'inf' || iv.hi.kind === 'inf') {
      throw new Error(`gridPoints: measurement ${m.id} has unknown error; domain is unbounded`);
    }
    const step = iv.hi.v.sub(iv.lo.v).div(Rational.fromInt(divisions));
    const values: Rational[] = [];
    for (let k = 0; k <= divisions; k++) values.push(iv.lo.v.add(step.mul(Rational.fromInt(k))));
    total *= values.length;
    if (total > maxPoints) {
      throw new Error(`gridPoints: grid too large (${total} points > ${maxPoints})`);
    }
    axes.push({ id: m.id, values });
  }
  const points: Assignment[] = [];
  const acc = new Map<string, Rational>();
  const walk = (i: number): void => {
    if (i === axes.length) {
      points.push(new Map(acc));
      return;
    }
    for (const v of axes[i].values) {
      acc.set(axes[i].id, v);
      walk(i + 1);
    }
  };
  walk(0);
  return points;
}

export function enumerateVerify(
  constraints: readonly LinearConstraint[],
  points: readonly Assignment[],
  maxSamples = 8,
): EnumerationResult {
  const per = new Map<string, { pass: number; fail: number }>();
  for (const c of constraints) per.set(c.id, { pass: 0, fail: 0 });
  let allSatisfied = 0;
  const violatingSamples: Assignment[] = [];
  for (const p of points) {
    let ok = true;
    for (const c of constraints) {
      const s = satisfies(c, p);
      const entry = per.get(c.id)!;
      if (s) entry.pass++;
      else {
        entry.fail++;
        ok = false;
      }
    }
    if (ok) allSatisfied++;
    else if (violatingSamples.length < maxSamples) violatingSamples.push(p);
  }
  return { totalPoints: points.length, allSatisfied, perConstraint: per, violatingSamples };
}
