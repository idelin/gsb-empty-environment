/**
 * Exact solver for the exception-revocation problem.
 *
 * Given a base policy plus temporary exceptions (first-match by priority),
 * find a minimum-total-cost set of exceptions to revoke so that a safety
 * property holds over the entire finite input domain.
 *
 * Key model fact: base rules and exceptions share one priority order, so a
 * request's decision sequence is the merged list of matching base rules and
 * exceptions, terminated by the policy default. Revoking an exception
 * removes it from that sequence; the first surviving entry decides.
 *
 * Two engines are provided:
 *  - solveFast:  exact polynomial solver for a single safety property.
 *    With one property, every constrained request forces a *conjunction* of
 *    revocations (all unsafe exceptions ranked above the first safe entry
 *    in its decision sequence), so the optimum is the union of mandatory
 *    revocations. Each revoked exception carries a necessity witness.
 *  - solveExact: general branch-and-bound over revocation/retention
 *    assignments (sound for multiple properties), used to cross-check
 *    solveFast and available via `--check`.
 */

import {
  enumerateRequests,
  matches,
  orderRules,
} from "./model.ts";
import type {
  Effect,
  ExceptionRule,
  Request,
  SafetyProperty,
  Scenario,
} from "./model.ts";

/** One entry in a request's decision sequence (priority order). */
export type ChainEntry =
  | { kind: "exception"; index: number }
  | { kind: "base"; id: string; safe: boolean };

export interface Constraint {
  /** A request (matching some property) whose decision may depend on exceptions. */
  request: Request;
  /**
   * Full decision sequence: matching exceptions and base rules in evaluation
   * order, terminated by a base entry for the policy default.
   */
  chain: ChainEntry[];
  /** The effect the property demands for this request. */
  mustBe: Effect;
  /** Index into the properties array. */
  propertyIndex: number;
}

export interface ConstraintSet {
  constraints: Constraint[];
  /** Property-relevant requests currently decided unsafely (all exceptions active). */
  violationsBefore: number;
  /** Property-relevant request count over the whole domain. */
  relevantRequests: number;
  totalRequests: number;
}

/** Build per-request constraints by scanning the entire finite input domain. */
export function buildConstraints(
  scenario: Scenario,
  properties: readonly SafetyProperty[] = [scenario.property],
): ConstraintSet {
  const { policy } = scenario;
  const exceptions = policy.exceptions;
  const indexOf = new Map<string, number>(exceptions.map((ex, i) => [ex.id, i]));
  const requests = enumerateRequests(scenario.domains);

  const constraints: Constraint[] = [];
  let violationsBefore = 0;
  let relevantRequests = 0;

  for (const request of requests) {
    // Merged decision sequence shared by all properties (safe flags differ).
    const merged: { id: string; exceptionIndex: number | null; priority: number }[] = [];
    for (const rule of orderRules([...policy.baseRules, ...exceptions])) {
      if (!matches(rule.when, request)) continue;
      const idx = indexOf.get(rule.id);
      merged.push({ id: rule.id, exceptionIndex: idx === undefined ? null : idx, priority: rule.priority });
    }

    for (let p = 0; p < properties.length; p++) {
      const prop = properties[p];
      if (!matches(prop.when, request)) continue;
      relevantRequests++;

      const chain: ChainEntry[] = merged.map((m) =>
        m.exceptionIndex === null
          ? { kind: "base", id: m.id, safe: safeBaseEffect(policy.baseRules, m.id, prop.mustBe) }
          : { kind: "exception", index: m.exceptionIndex },
      );
      chain.push({ kind: "base", id: "<default>", safe: policy.defaultEffect === prop.mustBe });

      const first = chain[0];
      const currentEffect: Effect =
        first.kind === "exception"
          ? exceptions[first.index].effect
          : first.id === "<default>"
            ? policy.defaultEffect
            : (policy.baseRules.find((r) => r.id === first.id)?.effect ?? policy.defaultEffect);
      if ((currentEffect === prop.mustBe) !== true) violationsBefore++;

      // Trivially satisfied regardless of revocation choices: every entry
      // that could ever decide is already safe.
      const alwaysSafe = chain.every((e) =>
        e.kind === "exception" ? exceptions[e.index].effect === prop.mustBe : e.safe,
      );
      if (!alwaysSafe) {
        constraints.push({ request, chain, mustBe: prop.mustBe, propertyIndex: p });
      }
    }
  }
  return { constraints, violationsBefore, relevantRequests, totalRequests: requests.length };
}

function safeBaseEffect(baseRules: readonly { id: string; effect: Effect }[], id: string, mustBe: Effect): boolean {
  const rule = baseRules.find((r) => r.id === id);
  return rule !== undefined && rule.effect === mustBe;
}

export interface RevocationPlan {
  status: "optimal";
  /** Exception indices to revoke, ascending. */
  revoked: number[];
  totalCost: number;
  /** Per revoked exception index: a request proving its revocation is necessary. */
  witnesses: Map<number, Request>;
  stats: { constraints: number; nodesExplored: number };
}

export interface Infeasible {
  status: "infeasible";
  reason: string;
  request?: Request;
}

export type SolveOutcome = RevocationPlan | Infeasible;

function infeasible(reason: string, request?: Request): Infeasible {
  return request === undefined ? { status: "infeasible", reason } : { status: "infeasible", reason, request };
}

/**
 * Exact solver for a single safety property.
 *
 * For each constrained request, walk its decision sequence in priority
 * order: every unsafe exception met before the first safe entry (a safe
 * exception, a safe base rule, or a safe default) must be revoked —
 * otherwise it, or an earlier unsafe exception, becomes the first surviving
 * entry and decides the request unsafely. Requirements are pure
 * conjunctions, so the union over all requests is the unique minimal
 * feasible set, hence cost-minimal. Base entries are terminal: a request
 * whose sequence reaches an unsafe base rule or unsafe default before any
 * safe entry can never be fixed by revocation, so it is infeasible.
 */
export function solveFast(scenario: Scenario): SolveOutcome {
  const exceptions = scenario.policy.exceptions;
  const { constraints } = buildConstraints(scenario, [scenario.property]);
  const mustBe = scenario.property.mustBe;

  const required = new Set<number>();
  const witnesses = new Map<number, Request>();

  for (const c of constraints) {
    let sawSafeEntry = false;
    for (const entry of c.chain) {
      if (entry.kind === "base") {
        // Base entries are terminal: entries below a base rule never decide
        // the request. A safe base entry completes the constraint; an unsafe
        // one cannot be revoked, so nothing below it can help.
        sawSafeEntry = entry.safe;
        break;
      }
      if (exceptions[entry.index].effect === mustBe) {
        sawSafeEntry = true;
        break;
      }
      required.add(entry.index);
      if (!witnesses.has(entry.index)) witnesses.set(entry.index, c.request);
    }
    if (!sawSafeEntry) {
      return infeasible(
        "request cannot be made safe by revoking exceptions: an unrevocable base rule or the default decides it unsafely",
        c.request,
      );
    }
  }

  const revoked = [...required].sort((a, b) => a - b);
  const totalCost = revoked.reduce((sum, i) => sum + exceptions[i].cost, 0);
  return {
    status: "optimal",
    revoked,
    totalCost,
    witnesses,
    stats: { constraints: constraints.length, nodesExplored: 1 },
  };
}

export interface ExactOptions {
  /** Safety cap on search nodes; solveExact returns "unknown" when exceeded. */
  maxNodes?: number;
}

export interface Unknown {
  status: "unknown";
  reason: string;
}

export type ExactOutcome = RevocationPlan | Infeasible | Unknown;

/**
 * General exact branch-and-bound over (revoked, kept) assignments.
 * Sound for one or many safety properties. Used to cross-check solveFast.
 */
export function solveExact(
  scenario: Scenario,
  properties: readonly SafetyProperty[] = [scenario.property],
  options: ExactOptions = {},
): ExactOutcome {
  const exceptions = scenario.policy.exceptions;
  const n = exceptions.length;
  const { constraints } = buildConstraints(scenario, properties);
  const maxNodes = options.maxNodes ?? 5_000_000;

  // Immediate infeasibility: a constrained request with no safe entry at all.
  for (const c of constraints) {
    const anySafe = c.chain.some((e) =>
      e.kind === "exception" ? exceptions[e.index].effect === c.mustBe : e.safe,
    );
    if (!anySafe) {
      return infeasible("no safe rule or default can ever decide this request", c.request);
    }
  }

  const bit = (i: number): number => 1 << i;
  let best = Infinity;
  let bestMask = 0;
  let nodes = 0;
  let overflow = false;

  const dfs = (revoked: number, kept: number, cost: number): void => {
    if (overflow) return;
    if (++nodes > maxNodes) {
      overflow = true;
      return;
    }
    if (cost >= best) return;

    let lowerBound = cost;
    let branchConstraint = -1;
    let branchFirst = -1;

    for (let ci = 0; ci < constraints.length; ci++) {
      const c = constraints[ci];
      let firstException = -1;
      let resolved = false;
      let dead = false;
      for (const entry of c.chain) {
        if (entry.kind === "base") {
          if (entry.safe) resolved = true;
          else dead = true;
          break;
        }
        if (revoked & bit(entry.index)) continue;
        firstException = entry.index;
        break;
      }
      if (dead) return;
      if (resolved) continue;
      if (firstException === -1) continue; // unreachable: default entry terminates the chain
      if (kept & bit(firstException)) {
        if (exceptions[firstException].effect === c.mustBe) continue;
        return; // retained unsafe decider
      }
      // Open constraint: first surviving entry is an undecided exception.
      if (exceptions[firstException].effect !== c.mustBe) {
        lowerBound = Math.max(lowerBound, cost + exceptions[firstException].cost);
      }
      if (branchConstraint === -1) {
        branchConstraint = ci;
        branchFirst = firstException;
      }
    }

    if (lowerBound >= best) return;
    if (branchConstraint === -1) {
      best = cost;
      bestMask = revoked;
      return;
    }

    const c = constraints[branchConstraint];
    const f = branchFirst;
    if (exceptions[f].effect === c.mustBe) {
      // Branch 1: retain f (free, satisfies this constraint).
      dfs(revoked, kept | bit(f), cost);
      // Branch 2: revoke f (may be required by other constraints).
      dfs(revoked | bit(f), kept, cost + exceptions[f].cost);
    } else {
      // Unsafe decider must be revoked.
      dfs(revoked | bit(f), kept, cost + exceptions[f].cost);
    }
  };

  dfs(0, 0, 0);

  if (overflow) return { status: "unknown", reason: `node budget ${maxNodes} exceeded` };
  if (best === Infinity) return infeasible("no revocation set restores the property");

  const revoked: number[] = [];
  for (let i = 0; i < n; i++) if (bestMask & bit(i)) revoked.push(i);
  return {
    status: "optimal",
    revoked,
    totalCost: best,
    witnesses: new Map(),
    stats: { constraints: constraints.length, nodesExplored: nodes },
  };
}

/** Human-readable request rendering for reports. */
export function formatRequest(req: Request): string {
  return Object.entries(req)
    .map(([k, v]) => `${k}=${v}`)
    .join(", ");
}

export function retainedExceptions(scenario: Scenario, revoked: readonly number[]): ExceptionRule[] {
  const drop = new Set(revoked);
  return scenario.policy.exceptions.filter((_, i) => !drop.has(i));
}
