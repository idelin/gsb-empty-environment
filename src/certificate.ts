/**
 * Independent solution certificates.
 *
 * A certificate captures everything needed to re-check a solve WITHOUT
 * trusting the solver's arithmetic:
 *  - the problem hash (any objective/instance change => "stale"),
 *  - the winning assignment, whose cost and worst-case error are recomputed
 *    from scratch by the verifier,
 *  - the full deterministic node log; the verifier replays the exact same
 *    deterministic branch & bound and requires every logged bound and every
 *    prune decision to reproduce exactly, then re-derives the reported
 *    status/upper/lower bounds.
 *
 * Re-validation after an objective change: verifyCertificate() is meant to be
 * called on the (possibly modified) problem with an old certificate. It
 * reports `stale: true` when the hash no longer matches and still re-checks
 * whatever it can; a stale certificate never certifies optimality.
 */

import * as R from "./rational.ts";
import { analyze, problemHash, totalCost } from "./model.ts";
import type { Problem } from "./model.ts";
import { solve } from "./solver.ts";
import type { NodeLogEntry, SolveResult } from "./solver.ts";

export const CERT_FORMAT = "wce-bnb-cert/1";

export interface Certificate {
  format: typeof CERT_FORMAT;
  problemHash: string;
  objective: "minimize-total-grade-cost";
  /** node budget the logged run used; the verifier replays with the same budget */
  maxNodes: number;
  status: SolveResult["status"];
  upperBound: string | null;
  lowerBound: string | null;
  bestAssignment: Record<string, string> | null;
  nodesExplored: number;
  budgetExhausted: boolean;
  log: NodeLogEntry[];
}

export function buildCertificate(problem: Problem, result: SolveResult, maxNodes = 1_000_000): Certificate {
  const bestAssignment: Record<string, string> | null = result.bestAssignment
    ? Object.fromEntries(
        [...result.bestAssignment].map(([ci, gi]) => [problem.components[ci].id, problem.components[ci].grades[gi].id]),
      )
    : null;
  return {
    format: CERT_FORMAT,
    problemHash: problemHash(problem),
    objective: "minimize-total-grade-cost",
    maxNodes,
    status: result.status,
    upperBound: result.upperBound === null ? null : R.toCanonical(result.upperBound),
    lowerBound: result.lowerBound === null ? null : R.toCanonical(result.lowerBound),
    bestAssignment,
    nodesExplored: result.nodesExplored,
    budgetExhausted: result.budgetExhausted,
    log: result.log,
  };
}

export interface Verification {
  valid: boolean;
  /** true when the certificate was issued for a different problem/objective */
  stale: boolean;
  reasons: string[];
}

export function verifyCertificate(problem: Problem, cert: Certificate): Verification {
  const reasons: string[] = [];
  if (cert.format !== CERT_FORMAT) {
    return { valid: false, stale: false, reasons: [`unknown format ${JSON.stringify(cert.format)}`] };
  }

  const stale = cert.problemHash !== problemHash(problem);
  if (stale) {
    reasons.push(
      `problem hash mismatch: certificate was issued for ${cert.problemHash}, ` +
        `current problem hashes to ${problemHash(problem)} — objective or instance changed; ` +
        `this certificate no longer certifies anything about the current problem`,
    );
  }

  // 1. Independently recompute the claimed solution's cost and feasibility.
  if (cert.bestAssignment) {
    const assignment = new Map<number, number>();
    for (const [compId, gradeId] of Object.entries(cert.bestAssignment)) {
      const ci = problem.componentIndex.get(compId);
      if (ci === undefined) {
        reasons.push(`solution references unknown component ${compId}`);
        continue;
      }
      const gi = problem.components[ci].grades.findIndex((g) => g.id === gradeId);
      if (gi === -1) {
        reasons.push(`solution references unknown grade ${compId}/${gradeId}`);
        continue;
      }
      assignment.set(ci, gi);
    }
    if (assignment.size === problem.components.length) {
      const cost = totalCost(problem, assignment);
      if (cert.upperBound === null || !R.eq(cost, R.parse(cert.upperBound))) {
        reasons.push(
          `recomputed solution cost ${R.toCanonical(cost)} != certified upper bound ${cert.upperBound}`,
        );
      }
      const a = analyze(problem, assignment);
      if (!a.feasible) {
        reasons.push(
          `certified solution is INFEASIBLE: worst-case error ${R.toCanonical(a.worst)} exceeds limit ${R.toCanonical(problem.errorLimit)}`,
        );
      }
    } else {
      reasons.push("certified solution does not assign every component");
    }
  } else if (cert.upperBound !== null) {
    reasons.push("certificate has an upper bound but no assignment");
  }

  // 2. Replay the deterministic search and require an exact match of the log.
  const replay = solve(problem, { maxNodes: cert.maxNodes, logNodes: true });
  const logsMatch =
    replay.log.length === cert.log.length &&
    replay.log.every((e, i) => JSON.stringify(e) === JSON.stringify(cert.log[i]));
  if (!logsMatch) {
    reasons.push(
      `node log does not reproduce under deterministic replay (cert has ${cert.log.length} entries, replay produced ${replay.log.length})`,
    );
  } else {
    if (replay.status !== cert.status) reasons.push(`status mismatch: replay=${replay.status} cert=${cert.status}`);
    const rb = replay.upperBound === null ? null : R.toCanonical(replay.upperBound);
    const rl = replay.lowerBound === null ? null : R.toCanonical(replay.lowerBound);
    if (rb !== cert.upperBound) reasons.push(`upper bound mismatch: replay=${rb} cert=${cert.upperBound}`);
    if (rl !== cert.lowerBound) reasons.push(`lower bound mismatch: replay=${rl} cert=${cert.lowerBound}`);
  }

  // 3. Structural honesty checks.
  if (cert.budgetExhausted && cert.status === "optimal") {
    reasons.push("budget was exhausted but the certificate claims optimality — not allowed");
  }
  if (cert.upperBound !== null && cert.lowerBound !== null) {
    if (R.cmp(R.parse(cert.lowerBound), R.parse(cert.upperBound)) > 0) {
      reasons.push(`lower bound ${cert.lowerBound} exceeds upper bound ${cert.upperBound}`);
    }
  }

  return { valid: reasons.length === 0, stale, reasons };
}
