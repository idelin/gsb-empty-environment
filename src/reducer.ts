import { ReplayLog, hashSteps } from "./replay-log";
import { ReplayHarness, ReplayStatus, ReplaySummary, Step } from "./types";
import { ValidationResult } from "./validate";

export interface ReducerOptions {
  harness: ReplayHarness;
  validate: (steps: Step[]) => ValidationResult;
  log?: ReplayLog;
  /** Safety cap on the number of replays. */
  maxReplays?: number;
}

export interface ReductionReport {
  originalLength: number;
  reducedLength: number;
  reduced: Step[];
  totalReplays: number;
  counts: Record<ReplayStatus, number>;
  /** True when every single-step deletion of the result fails to reproduce. */
  verifiedOneMinimal: boolean;
}

export class ReductionError extends Error {}

function withoutIndices(steps: Step[], removed: number[]): Step[] {
  const drop = new Set(removed);
  return steps.filter((_, i) => !drop.has(i));
}

/**
 * Reduce a failing script to a 1-minimal one: the result still reproduces
 * the assertion failure, and deleting any single remaining step no longer
 * does. Candidates that fail validation are recorded as "invalid" and are
 * never accepted as reductions.
 */
export function reduceScript(original: Step[], opts: ReducerOptions): ReductionReport {
  const log = opts.log ?? new ReplayLog();
  const maxReplays = opts.maxReplays ?? 1000;
  const counts: Record<ReplayStatus, number> = { fail: 0, pass: 0, invalid: 0 };

  const attempt = (steps: Step[], action: "initial" | "ddmin" | "greedy" | "verify", removedIndices?: number[]): ReplayStatus => {
    if (log.count >= maxReplays) {
      throw new ReductionError(`replay budget exhausted (${maxReplays})`);
    }
    let status: ReplayStatus;
    let summary: ReplaySummary;
    const validation = opts.validate(steps);
    if (!validation.ok) {
      status = "invalid";
      summary = {
        status,
        invalidReason: validation.errors.join("; "),
        simulatedMs: 0,
        events: 0,
        finalLeaders: [],
        finalTerms: {},
      };
    } else {
      summary = opts.harness.replay(steps);
      status = summary.status;
    }
    counts[status] += 1;
    log.record({
      action,
      scriptLength: steps.length,
      scriptHash: hashSteps(steps),
      removedIndices,
      status,
      summary,
    });
    return status;
  };

  if (attempt(original.slice(), "initial") !== "fail") {
    throw new ReductionError(
      "input script does not reproduce the failure; refusing to reduce"
    );
  }

  let current = original.slice();

  // Phase 1: ddmin-style chunk elimination.
  let n = 2;
  while (current.length >= 2) {
    const chunkSize = Math.ceil(current.length / Math.min(n, current.length));
    let accepted = false;
    for (let start = 0; start < current.length; start += chunkSize) {
      const removed: number[] = [];
      for (let i = start; i < Math.min(start + chunkSize, current.length); i++) removed.push(i);
      const candidate = withoutIndices(current, removed);
      if (candidate.length === 0) continue;
      if (attempt(candidate, "ddmin", removed) === "fail") {
        current = candidate;
        n = 2;
        accepted = true;
        break;
      }
    }
    if (!accepted) {
      if (n >= current.length) break;
      n = Math.min(n * 2, current.length);
    }
  }

  // Phase 2: greedy single-step elimination to a fixpoint (1-minimality).
  let changed = true;
  while (changed) {
    changed = false;
    for (let i = 0; i < current.length; i++) {
      const candidate = withoutIndices(current, [i]);
      if (candidate.length === 0) continue;
      if (attempt(candidate, "greedy", [i]) === "fail") {
        current = candidate;
        changed = true;
        break;
      }
    }
  }

  // Phase 3: verify — every single-step deletion must NOT reproduce.
  let verifiedOneMinimal = true;
  for (let i = 0; i < current.length; i++) {
    const candidate = withoutIndices(current, [i]);
    if (candidate.length === 0) continue;
    if (attempt(candidate, "verify", [i]) === "fail") {
      verifiedOneMinimal = false;
    }
  }
  if (!verifiedOneMinimal) {
    throw new ReductionError(
      "non-deterministic harness: a single-step deletion reproduced the failure during verification"
    );
  }

  return {
    originalLength: original.length,
    reducedLength: current.length,
    reduced: current,
    totalReplays: log.count,
    counts,
    verifiedOneMinimal,
  };
}
