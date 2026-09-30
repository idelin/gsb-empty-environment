/**
 * Solution certificates and their independent verification.
 *
 * A certificate records: instance hash (covers the objective, i.e. the
 * error budget), the selection, exact cost, the full per-source error
 * breakdown, the proven lower bound, and solver stats.
 *
 * Verification is independent: it re-parses nothing from the solver, it
 * recomputes cost/error/constraints from the instance alone, checks the
 * hash (any objective change => stale => invalid), and — when the instance
 * is small enough — re-proves optimality by exhaustive enumeration rather
 * than trusting the B&B stats.
 */
import { ZERO, add, toExactString, cmp } from './rational.ts';
import type { Rational } from './rational.ts';
import { instanceHash } from './model.ts';
import type { Instance } from './model.ts';
import { evaluateError, checkConstraints } from './error.ts';
import type { ErrorReport } from './error.ts';
import type { SolveResult } from './solve.ts';
import { exhaustiveSolve } from './exhaustive.ts';

export interface SourceProof {
  readonly sourceId: string;
  readonly kind: string;
  readonly signedSum: string; // exact rational string
  readonly worstCase: string;
  readonly terms: readonly {
    readonly componentId: string;
    readonly tierId: string;
    readonly sensitivity: string;
    readonly tierError: string;
    readonly signedContribution: string;
  }[];
}

export interface Certificate {
  readonly version: 1;
  readonly instanceHash: string;
  readonly instanceName: string;
  readonly status: 'optimal' | 'infeasible' | 'undecided';
  readonly selection: Record<string, string> | null;
  /** exact cost of the selection (upper bound on optimum) */
  readonly upperBoundCost: string | null;
  /** proven lower bound on optimal cost; null = +infinity (proven infeasible) */
  readonly lowerBoundCost: string | null;
  readonly errorTotal: string | null;
  readonly errorBudget: string;
  readonly errorBreakdown: readonly SourceProof[] | null;
  readonly solver: {
    readonly method: 'branch-and-bound';
    readonly nodesExplored: number;
    readonly nodesPrunedCost: number;
    readonly nodesPrunedError: number;
    readonly leavesEvaluated: number;
    readonly budgetExhausted: boolean;
  };
  readonly note: string;
}

function breakdown(report: ErrorReport): SourceProof[] {
  return report.perSource.map((s) => ({
    sourceId: s.sourceId,
    kind: s.kind,
    signedSum: toExactString(s.signedSum),
    worstCase: toExactString(s.worstCase),
    terms: s.terms.map((t) => ({
      componentId: t.componentId,
      tierId: t.tierId,
      sensitivity: toExactString(t.sensitivity),
      tierError: toExactString(t.tierError),
      signedContribution: toExactString(t.signedContribution),
    })),
  }));
}

export function issueCertificate(inst: Instance, result: SolveResult): Certificate {
  const sel = result.incumbent?.selection ?? null;
  return {
    version: 1,
    instanceHash: instanceHash(inst),
    instanceName: inst.name,
    status: result.status,
    selection: sel ? Object.fromEntries(sel) : null,
    upperBoundCost: result.incumbent ? toExactString(result.incumbent.cost) : null,
    lowerBoundCost: result.lowerBound ? toExactString(result.lowerBound) : null,
    errorTotal: result.incumbent ? toExactString(result.incumbent.error.total) : null,
    errorBudget: toExactString(inst.errorBudget),
    errorBreakdown: result.incumbent ? breakdown(result.incumbent.error) : null,
    solver: {
      method: 'branch-and-bound',
      nodesExplored: result.nodesExplored,
      nodesPrunedCost: result.nodesPrunedCost,
      nodesPrunedError: result.nodesPrunedError,
      leavesEvaluated: result.leavesEvaluated,
      budgetExhausted: result.budgetExhausted,
    },
    note:
      result.status === 'undecided'
        ? 'node budget exhausted: bounds reported, optimality NOT claimed'
        : result.status === 'infeasible'
          ? 'search completed: no feasible selection exists'
          : 'search completed: cost proven optimal',
  };
}

export interface VerifyCheck {
  readonly name: string;
  readonly ok: boolean;
  readonly detail: string;
}

export interface VerifyResult {
  readonly valid: boolean;
  readonly stale: boolean;
  readonly checks: readonly VerifyCheck[];
  readonly optimalityProof: 'exhaustive-confirmed' | 'not-claimed' | 'skipped-too-large' | 'failed';
}

export function verifyCertificate(
  inst: Instance,
  cert: Certificate,
  exhaustiveLimit = 200_000,
): VerifyResult {
  const checks: VerifyCheck[] = [];
  const push = (name: string, ok: boolean, detail: string) => checks.push({ name, ok, detail });

  // 1. Hash: covers the objective. Changed budget => stale certificate.
  const hash = instanceHash(inst);
  const stale = hash !== cert.instanceHash;
  push(
    'instance-hash',
    !stale,
    stale
      ? `instance or objective changed since issuance (cert ${cert.instanceHash.slice(0, 12)}… vs current ${hash.slice(0, 12)}…); re-solve required`
      : 'certificate matches current instance and objective',
  );

  // 2. Status consistency.
  const statusKnown = cert.status === 'optimal' || cert.status === 'infeasible' || cert.status === 'undecided';
  push('status-enum', statusKnown, `status=${cert.status}`);
  if (cert.status === 'undecided') {
    push('no-optimality-claim', true, 'undecided: bounds only, optimality not claimed');
  }

  // 3. Selection checks (when present).
  let recomputedCost: Rational | null = null;
  if (cert.selection !== null) {
    const compIds = new Set(inst.components.map((c) => c.id));
    const selKeys = Object.keys(cert.selection);
    const covers =
      selKeys.length === compIds.size && selKeys.every((k) => compIds.has(k));
    push('selection-covers-components', covers, covers ? 'all components assigned' : 'component set mismatch');

    let selection: Map<string, string> | null = null;
    let tiersOk = false;
    if (covers) {
      selection = new Map(Object.entries(cert.selection));
      tiersOk = inst.components.every((c) => {
        const t = selection!.get(c.id);
        return t !== undefined && c.tiers.some((x) => x.id === t);
      });
    }
    push('tiers-exist', tiersOk, tiersOk ? 'all referenced tiers exist' : 'unknown tier referenced');

    if (selection && tiersOk) {
      const violations = checkConstraints(inst, selection);
      push(
        'constraints',
        violations.length === 0,
        violations.length === 0 ? 'exclusions and dependencies satisfied' : violations.join('; '),
      );

      const error = evaluateError(inst, selection);
      push(
        'error-within-budget',
        error.feasible,
        `worst-case total ${toExactString(error.total)} vs budget ${toExactString(inst.errorBudget)}`,
      );
      push(
        'error-matches-certificate',
        cert.errorTotal !== null && toExactString(error.total) === cert.errorTotal,
        `certified ${cert.errorTotal ?? 'none'} vs recomputed ${toExactString(error.total)}`,
      );

      let cost: Rational = ZERO;
      for (const c of inst.components) {
        const tier = c.tiers.find((t) => t.id === selection!.get(c.id))!;
        cost = add(cost, tier.cost);
      }
      recomputedCost = cost;
      push(
        'cost-matches-certificate',
        cert.upperBoundCost !== null && toExactString(cost) === cert.upperBoundCost,
        `certified ${cert.upperBoundCost ?? 'none'} vs recomputed ${toExactString(cost)}`,
      );
    }
  } else {
    push('selection-present', cert.status !== 'optimal', 'no selection (only valid if not optimal)');
  }

  // 4. Bound consistency.
  if (cert.lowerBoundCost !== null && cert.upperBoundCost !== null) {
    const lbR = parseExact(cert.lowerBoundCost);
    const ubR = parseExact(cert.upperBoundCost);
    push('bounds-ordered', cmp(lbR, ubR) <= 0, `lower ${cert.lowerBoundCost} <= upper ${cert.upperBoundCost}`);
    if (cert.status === 'optimal') {
      push('optimal-bounds-tight', cmp(lbR, ubR) === 0, 'optimal: lower bound equals achieved cost');
    }
  } else if (cert.status === 'infeasible') {
    push('bounds-infeasible', cert.lowerBoundCost === null && cert.upperBoundCost === null, 'infeasible: no finite bounds');
  }

  // 5. Independent optimality proof for small instances.
  let optimalityProof: VerifyResult['optimalityProof'] = 'not-claimed';
  if (!stale && cert.status === 'optimal' && recomputedCost !== null) {
    const combos = inst.components.reduce((p, c) => p * c.tiers.length, 1);
    if (combos <= exhaustiveLimit) {
      const ex = exhaustiveSolve(inst, exhaustiveLimit);
      const confirmed = ex.best !== null && cmp(ex.best.cost, recomputedCost) === 0;
      optimalityProof = confirmed ? 'exhaustive-confirmed' : 'failed';
      push(
        'independent-exhaustive-proof',
        confirmed,
        `exhaustive best ${ex.best ? toExactString(ex.best.cost) : 'none'} over ${ex.totalCombinations} combinations`,
      );
    } else {
      optimalityProof = 'skipped-too-large';
      push('independent-exhaustive-proof', true, `skipped: ${combos} combinations exceed limit ${exhaustiveLimit}`);
    }
  }

  const valid = checks.every((c) => c.ok);
  return { valid, stale, checks, optimalityProof };
}

function gcd(a: bigint, b: bigint): bigint {
  let x = a < 0n ? -a : a;
  let y = b < 0n ? -b : b;
  while (y !== 0n) {
    const t = x % y;
    x = y;
    y = t;
  }
  return x === 0n ? 1n : x;
}

/** Parse "num" or "num/den" exact strings (as emitted by toExactString). */
function parseExact(s: string): Rational {
  const slash = s.indexOf('/');
  if (slash === -1) return { num: BigInt(s), den: 1n };
  return makeNormalized(BigInt(s.slice(0, slash)), BigInt(s.slice(slash + 1)));
}

function makeNormalized(num: bigint, den: bigint): Rational {
  if (den === 0n) throw new Error('zero denominator in certificate');
  let n = num;
  let d = den;
  if (d < 0n) {
    n = -n;
    d = -d;
  }
  const g = gcd(n, d);
  return { num: n / g, den: d / g };
}
