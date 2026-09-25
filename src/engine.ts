import {
  deepClone,
  deepEqual,
  deepFreeze,
  stateHash,
  type JsonValue,
} from "./json.ts";
import { getAt, hasAt, setAt } from "./pointer.ts";

/** One proposed rewrite at a concrete JSON Pointer path. */
export interface RuleMatch {
  /** JSON Pointer of the node to rewrite. */
  path: string;
  /** Value to write at `path`. */
  replacement: JsonValue;
  /**
   * Precondition guard: the edit is executed only if the current value at
   * `path` deep-equals `expect` at apply time. Re-checked by the engine on
   * every iteration, so stale matches are never executed.
   */
  expect?: JsonValue;
  note?: string;
}

export interface FixRule {
  id: string;
  /** Higher priority is evaluated first; ties broken by rule id. */
  priority: number;
  /** Inspect the state and return proposed rewrites (empty = not applicable). */
  match(state: JsonValue): RuleMatch[];
}

export interface JournalEntry {
  step: number;
  ruleId: string;
  path: string;
  before: JsonValue;
  after: JsonValue;
}

export type SkipReason = "invalid-pointer" | "path-not-found" | "precondition-not-met";

export interface SkippedApplication {
  atStep: number;
  ruleId: string;
  path: string;
  reason: SkipReason;
  expected?: JsonValue;
  actual?: JsonValue;
}

export interface ConflictEvidence {
  kind: "conflict";
  path: string;
  current: JsonValue;
  proposals: Array<{ ruleId: string; replacement: JsonValue }>;
  stateHash: string;
}

export interface OscillationEvidence {
  kind: "oscillation";
  /** Edits (step, rule, path) that lead from a state back to itself. */
  cycle: Array<{ step: number; ruleId: string; path: string }>;
  periodStartStep: number;
  repeatedStateHash: string;
}

export type TerminationReason = "fixpoint" | "budget" | "conflict" | "oscillation";

export interface EngineReport {
  termination: TerminationReason;
  steps: number;
  budget: number;
  journal: JournalEntry[];
  skipped: SkippedApplication[];
  conflict?: ConflictEvidence;
  oscillation?: OscillationEvidence;
  /** Working state at termination (NOT the input; use rollback() to restore). */
  state: JsonValue;
}

export interface EngineOptions {
  /** Maximum number of applied edits before forced termination. Default 1000. */
  maxSteps?: number;
}

interface RankedMatch extends RuleMatch {
  ruleId: string;
}

/**
 * Applies fix rules to a JSON tree until a fixpoint, the step budget, a
 * conflict, or an oscillation is reached. Every applied edit is journaled
 * with its inverse so the run is fully reversible.
 */
export class FixEngine {
  readonly #input: JsonValue;
  readonly #rules: FixRule[];
  readonly #maxSteps: number;
  readonly #journal: JournalEntry[] = [];
  readonly #skipped: SkippedApplication[] = [];
  #state: JsonValue;

  constructor(input: JsonValue, rules: FixRule[], options: EngineOptions = {}) {
    // The pristine input is cloned once and frozen; it is never mutated and
    // never normalized, so rollback restores the input byte-for-byte.
    this.#input = deepClone(input);
    deepFreeze(this.#input);
    this.#rules = [...rules].sort(
      (a, b) => b.priority - a.priority || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
    );
    this.#maxSteps = options.maxSteps ?? 1000;
    this.#state = deepClone(input);
  }

  run(): EngineReport {
    const seen = new Map<string, number>();
    seen.set(stateHash(this.#state), 0);
    let step = 0;

    for (;;) {
      const tier = this.#collectTier();
      if (tier.length === 0) {
        return this.#report("fixpoint", step);
      }
      if (step >= this.#maxSteps) {
        return this.#report("budget", step);
      }
      const conflict = this.#detectConflict(tier);
      if (conflict) {
        return this.#report("conflict", step, conflict);
      }

      const next = tier[0];
      const before = deepClone(getAt(this.#state, next.path));
      setAt(this.#state, next.path, deepClone(next.replacement));
      step += 1;
      this.#journal.push({
        step,
        ruleId: next.ruleId,
        path: next.path,
        before,
        after: deepClone(next.replacement),
      });

      const hash = stateHash(this.#state);
      const previousVisit = seen.get(hash);
      if (previousVisit !== undefined) {
        const cycle = this.#journal
          .slice(previousVisit)
          .map((entry) => ({ step: entry.step, ruleId: entry.ruleId, path: entry.path }));
        return this.#report("oscillation", step, {
          kind: "oscillation",
          cycle,
          periodStartStep: previousVisit + 1,
          repeatedStateHash: hash,
        });
      }
      seen.set(hash, step);
    }
  }

  /**
   * Restores the original input. The journal is replayed in reverse on the
   * working copy as an integrity check; the returned value is a deep clone
   * of the pristine input itself (original key order preserved), never a
   * canonicalized/normalized copy.
   */
  rollback(): JsonValue {
    const restored = deepClone(this.#state);
    for (let i = this.#journal.length - 1; i >= 0; i--) {
      const entry = this.#journal[i];
      setAt(restored, entry.path, deepClone(entry.before));
    }
    if (!deepEqual(restored, this.#input)) {
      throw new Error("Rollback integrity check failed: journal does not restore the input");
    }
    return deepClone(this.#input);
  }

  /** Highest-priority tier of applicable matches, preconditions re-checked. */
  #collectTier(): RankedMatch[] {
    let tierPriority: number | null = null;
    const collected: RankedMatch[] = [];
    for (const rule of this.#rules) {
      if (tierPriority !== null && rule.priority < tierPriority) break;
      const usable: RankedMatch[] = [];
      for (const candidate of rule.match(this.#state)) {
        if (this.#checkPrecondition(rule.id, candidate)) {
          usable.push({ ...candidate, ruleId: rule.id });
        }
      }
      if (usable.length > 0) {
        tierPriority = rule.priority;
        collected.push(...usable);
      }
    }
    collected.sort(
      (a, b) =>
        (a.ruleId < b.ruleId ? -1 : a.ruleId > b.ruleId ? 1 : 0) ||
        (a.path < b.path ? -1 : a.path > b.path ? 1 : 0),
    );
    return collected;
  }

  /** A match whose precondition does not hold is skipped, never executed. */
  #checkPrecondition(ruleId: string, candidate: RuleMatch): boolean {
    const atStep = this.#journal.length;
    let exists = false;
    try {
      exists = hasAt(this.#state, candidate.path);
    } catch {
      this.#skipped.push({ atStep, ruleId, path: candidate.path, reason: "invalid-pointer" });
      return false;
    }
    if (!exists) {
      this.#skipped.push({ atStep, ruleId, path: candidate.path, reason: "path-not-found" });
      return false;
    }
    if (candidate.expect !== undefined) {
      const actual = getAt(this.#state, candidate.path);
      if (!deepEqual(actual, candidate.expect)) {
        this.#skipped.push({
          atStep,
          ruleId,
          path: candidate.path,
          reason: "precondition-not-met",
          expected: candidate.expect,
          actual,
        });
        return false;
      }
    }
    return true;
  }

  /** Same path, same priority tier, different replacements => conflict. */
  #detectConflict(tier: RankedMatch[]): ConflictEvidence | undefined {
    const byPath = new Map<string, RankedMatch[]>();
    for (const match of tier) {
      const group = byPath.get(match.path);
      if (group) group.push(match);
      else byPath.set(match.path, [match]);
    }
    for (const [path, group] of byPath) {
      const distinct: JsonValue[] = [];
      for (const match of group) {
        if (!distinct.some((value) => deepEqual(value, match.replacement))) {
          distinct.push(match.replacement);
        }
      }
      if (distinct.length > 1) {
        return {
          kind: "conflict",
          path,
          current: getAt(this.#state, path),
          proposals: group.map((match) => ({
            ruleId: match.ruleId,
            replacement: match.replacement,
          })),
          stateHash: stateHash(this.#state),
        };
      }
    }
    return undefined;
  }

  #report(
    termination: TerminationReason,
    steps: number,
    evidence?: ConflictEvidence | OscillationEvidence,
  ): EngineReport {
    const report: EngineReport = {
      termination,
      steps,
      budget: this.#maxSteps,
      journal: this.#journal.map((entry) => deepClone(entry as unknown as JsonValue) as unknown as JournalEntry),
      skipped: this.#skipped.map((entry) => ({ ...entry })),
      state: deepClone(this.#state),
    };
    if (evidence?.kind === "conflict") report.conflict = evidence;
    if (evidence?.kind === "oscillation") report.oscillation = evidence;
    return report;
  }
}
