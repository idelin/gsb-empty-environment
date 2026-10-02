import { Fraction } from './fraction.ts';
import { checkFeasibility, tighten, type TightBounds } from './bounds.ts';
import { normalize, type NormalizedProblem, type SolveInput } from './model.ts';
import type { Objective, SearchStats, SolveResult } from './solver.ts';

export interface ConservationCertificate {
  total: string;
  allocatedSum: string;
  conserved: boolean;
}

export interface OptimalityCertificate {
  objective: { movement?: string; deviation: string };
  lowerBound: { movement?: string; deviation: string };
  boundHolds: boolean;
  method: 'largest-remainder-baseline' | 'exhaustive-enumeration';
  nodesExplored: number;
  feasibleCount: number;
}

export interface Certificate {
  conservation: ConservationCertificate;
  optimality: OptimalityCertificate;
}

const absB = (x: bigint): bigint => (x < 0n ? -x : x);
const maxB = (a: bigint, b: bigint): bigint => (a > b ? a : b);

/** Distance from q to the INTEGER points of [lo, hi] (integrality-aware). */
function distToInterval(q: Fraction, lo: bigint, hi: bigint): Fraction {
  const loF = new Fraction(lo, 1n);
  const hiF = new Fraction(hi, 1n);
  if (q.cmp(loF) < 0) return loF.sub(q);
  if (q.cmp(hiF) > 0) return q.sub(hiF);
  // Inside the interval: distance to the nearest integer within [lo, hi].
  const f = q.floor();
  const clamp = (v: bigint): bigint => (v < lo ? lo : v > hi ? hi : v);
  const c1 = new Fraction(clamp(f), 1n);
  const c2 = new Fraction(clamp(f + 1n), 1n);
  const d1 = q.sub(c1).abs();
  const d2 = q.sub(c2).abs();
  return d1.cmp(d2) <= 0 ? d1 : d2;
}

/**
 * Valid lower bound on sum|x_i - q_i| for ANY feasible integer allocation:
 *  LB1 = sum of per-item distances to the integer points of the
 *        (tightened) feasible intervals — ignores coupling, a relaxation;
 *  LB2 = 2 * max(total forced-up mass, total forced-down mass)
 *        — valid because conservation gives sum(x_i - q_i) = 0 exactly,
 *        so positive and negative deviations must balance.
 */
export function deviationLowerBound(
  problem: NormalizedProblem,
  bounds: TightBounds,
): Fraction {
  let lb1 = Fraction.zero();
  let forcedUp = Fraction.zero();
  let forcedDown = Fraction.zero();
  for (let i = 0; i < problem.items.length; i++) {
    const q = problem.ideal[i];
    lb1 = lb1.add(distToInterval(q, bounds.lo[i], bounds.hi[i]));
    const loF = new Fraction(bounds.lo[i], 1n);
    const hiF = new Fraction(bounds.hi[i], 1n);
    if (loF.cmp(q) > 0) forcedUp = forcedUp.add(loF.sub(q));
    if (q.cmp(hiF) > 0) forcedDown = forcedDown.add(q.sub(hiF));
  }
  const lb2 = (forcedUp.cmp(forcedDown) >= 0 ? forcedUp : forcedDown).mul(
    new Fraction(2n, 1n),
  );
  return lb1.cmp(lb2) >= 0 ? lb1 : lb2;
}

/**
 * Valid lower bound on sum|x_i - prev_i| for ANY feasible allocation:
 *  max( sum of per-item distances to feasible intervals,
 *       |total - sum(prev)| )  (triangle inequality on the total).
 */
export function movementLowerBound(
  problem: NormalizedProblem,
  bounds: TightBounds,
): bigint {
  const prev = problem.previous;
  if (prev === null) return 0n;
  let lb = 0n;
  let prevSum = 0n;
  for (let i = 0; i < problem.items.length; i++) {
    prevSum += prev[i];
    if (prev[i] < bounds.lo[i]) lb += bounds.lo[i] - prev[i];
    else if (prev[i] > bounds.hi[i]) lb += prev[i] - bounds.hi[i];
  }
  return maxB(lb, absB(problem.total - prevSum));
}

export function buildCertificate(
  problem: NormalizedProblem,
  allocation: Record<string, bigint>,
  objective: Objective,
  search: SearchStats,
): Certificate {
  const bounds = tighten(problem);
  let sum = 0n;
  for (const it of problem.items) sum += allocation[it.id];

  const devLB = deviationLowerBound(problem, bounds);
  const cert: Certificate = {
    conservation: {
      total: problem.total.toString(),
      allocatedSum: sum.toString(),
      conserved: sum === problem.total,
    },
    optimality: {
      objective: {
        deviation: objective.deviation.toString(),
        ...(objective.movement !== undefined
          ? { movement: objective.movement.toString() }
          : {}),
      },
      lowerBound: { deviation: devLB.toString() },
      boundHolds: devLB.cmp(objective.deviation) <= 0,
      method: 'exhaustive-enumeration',
      nodesExplored: search.nodesExplored,
      feasibleCount: search.feasibleCount,
    },
  };
  if (objective.movement !== undefined) {
    const movLB = movementLowerBound(problem, bounds);
    cert.optimality.lowerBound.movement = movLB.toString();
    cert.optimality.boundHolds =
      cert.optimality.boundHolds && movLB <= objective.movement;
  }
  return cert;
}

export interface VerificationCheck {
  name: string;
  ok: boolean;
  detail: string;
}

export interface Verification {
  ok: boolean;
  checks: VerificationCheck[];
}

/**
 * INDEPENDENT verifier: re-derives everything from the raw input and the
 * claimed result, sharing nothing with the search itself. Checks integer
 * conservation, every item/group bound, the reported objective values,
 * and that the optimality lower bound certificate is valid (LB <= objective).
 */
export function verifyCertificate(
  input: SolveInput,
  result: SolveResult,
): Verification {
  const checks: VerificationCheck[] = [];
  const push = (name: string, ok: boolean, detail: string): void => {
    checks.push({ name, ok, detail });
  };

  const norm = normalize(input);
  if (!norm.ok) {
    push('input-valid', false, norm.error);
    return { ok: false, checks };
  }
  const problem = norm.problem;
  push('input-valid', true, 'input normalized and validated');

  if (result.status === 'error') {
    push('result-status', false, `solver reported error: ${result.error}`);
    return { ok: false, checks };
  }

  const bounds = tighten(problem);
  const feasibility = checkFeasibility(problem, bounds);

  if (result.status === 'infeasible') {
    push(
      'infeasible-confirmed',
      !feasibility.feasible,
      feasibility.feasible
        ? 'independent check found the instance FEASIBLE'
        : `independent check confirms infeasibility: ${feasibility.violations.join('; ')}`,
    );
    push(
      'deficit-matches',
      result.deficit === feasibility.deficit,
      `reported deficit ${result.deficit}, independent deficit ${feasibility.deficit}`,
    );
    push(
      'pending-not-forced',
      result.pending === result.deficit,
      `shortfall ${result.pending} kept pending, no allocation forced`,
    );
    return { ok: checks.every((c) => c.ok), checks };
  }

  // --- optimal branch ---
  const alloc = result.allocation;
  let allBigint = true;
  let sum = 0n;
  for (const it of problem.items) {
    const v = alloc[it.id];
    if (typeof v !== 'bigint') {
      allBigint = false;
    } else {
      sum += v;
    }
  }
  push('integer-values', allBigint, 'every allocation is a bigint integer');
  push(
    'conservation',
    sum === problem.total,
    `sum ${sum} ${sum === problem.total ? '==' : '!='} total ${problem.total}`,
  );

  let itemsOk = true;
  for (let i = 0; i < problem.items.length; i++) {
    const it = problem.items[i];
    const v = alloc[it.id];
    if (typeof v === 'bigint' && (v < it.lower || v > it.upper)) {
      itemsOk = false;
      push('item-bounds', false, `item "${it.id}" = ${v} outside [${it.lower}, ${it.upper}]`);
    }
  }
  if (itemsOk) push('item-bounds', true, 'all item bounds respected');

  let groupsOk = true;
  for (const g of problem.groups) {
    let gsum = 0n;
    for (const m of g.memberIdx) gsum += alloc[problem.items[m].id] ?? 0n;
    if (g.lower !== null && gsum < g.lower) {
      groupsOk = false;
      push('group-bounds', false, `group "${g.id}" sum ${gsum} < lower ${g.lower}`);
    }
    if (g.upper !== null && gsum > g.upper) {
      groupsOk = false;
      push('group-bounds', false, `group "${g.id}" sum ${gsum} > upper ${g.upper}`);
    }
  }
  if (groupsOk) push('group-bounds', true, 'all (overlapping) group bounds respected');

  // Recompute the objective independently.
  let dev = Fraction.zero();
  for (let i = 0; i < problem.items.length; i++) {
    dev = dev.add(
      problem.ideal[i].sub(new Fraction(alloc[problem.items[i].id], 1n)).abs(),
    );
  }
  push(
    'objective-deviation',
    dev.eq(result.objective.deviation),
    `recomputed deviation ${dev} vs reported ${result.objective.deviation}`,
  );

  if (problem.previous !== null) {
    let mov = 0n;
    for (let i = 0; i < problem.items.length; i++) {
      mov += absB(alloc[problem.items[i].id] - problem.previous[i]);
    }
    push(
      'objective-movement',
      result.objective.movement !== undefined && mov === result.objective.movement,
      `recomputed movement ${mov} vs reported ${result.objective.movement}`,
    );
    const movLB = movementLowerBound(problem, bounds);
    push(
      'lower-bound-movement',
      result.objective.movement !== undefined && movLB <= result.objective.movement,
      `movement lower bound ${movLB} <= objective ${result.objective.movement}`,
    );
  }

  const devLB = deviationLowerBound(problem, bounds);
  push(
    'lower-bound-deviation',
    devLB.cmp(result.objective.deviation) <= 0,
    `deviation lower bound ${devLB} <= objective ${result.objective.deviation}`,
  );

  push(
    'certificate-conservation',
    result.certificate.conservation.conserved &&
      result.certificate.conservation.allocatedSum === sum.toString(),
    `certificate conservation: ${JSON.stringify(result.certificate.conservation)}`,
  );
  push(
    'certificate-bound',
    result.certificate.optimality.boundHolds,
    `certificate optimality: ${JSON.stringify(result.certificate.optimality.lowerBound)}`,
  );

  return { ok: checks.every((c) => c.ok), checks };
}
