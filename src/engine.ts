import {
  canonicalize,
  comparePaths,
  deepClone,
  formatPath,
  samePath,
  setAt,
  walk,
  type JsonValue,
  type Path,
} from './json.ts';

export interface MatchContext {
  /** Current working tree (never the caller's input object). */
  readonly root: JsonValue;
  /** Location of the candidate node. */
  readonly path: Path;
  /** Value at `path`. */
  readonly value: JsonValue;
}

export interface Rule {
  readonly name: string;
  /** Lower number = higher priority. */
  readonly priority: number;
  /** Gate: the rule MUST NOT fire unless this returns true for the node. */
  precondition(ctx: MatchContext): boolean;
  /** Returns the replacement value for the matched node. */
  fix(value: JsonValue, ctx: MatchContext): JsonValue;
}

/** One reversible journal entry: reapplying `before` at `path` undoes the step. */
export interface Step {
  readonly seq: number;
  readonly rule: string;
  readonly priority: number;
  /** RFC 6901 pointer, e.g. "/items/2/name". */
  readonly path: string;
  readonly segments: Path;
  readonly before: JsonValue;
  readonly after: JsonValue;
}

interface Candidate {
  readonly rule: Rule;
  readonly path: Path;
  readonly value: JsonValue;
}

export type Status = 'fixpoint' | 'budget-exhausted' | 'conflict' | 'oscillation';

/** Evidence for a same-path, same-priority ambiguity between distinct rules. */
export interface ConflictEvidence {
  readonly path: string;
  readonly value: JsonValue;
  readonly competitors: readonly { rule: string; priority: number }[];
}

/** Evidence for a detected cycle, e.g. rule A: x->y vs rule B: y->x. */
export interface OscillationEvidence {
  /** Canonical hash of the repeated state. */
  readonly stateHash: string;
  /** Sequence number of the first step of the cycle. */
  readonly cycleStartSeq: number;
  /** The steps that transform the state back into itself. */
  readonly cycleSteps: readonly Step[];
  readonly rulesInvolved: readonly string[];
}

export interface RunResult {
  readonly status: Status;
  /** Full ordered journal of applied rewrites (empty if none). */
  readonly steps: readonly Step[];
  /** Final working tree (a clone; the caller's input is never mutated). */
  readonly output: JsonValue;
  readonly conflict?: ConflictEvidence;
  readonly oscillation?: OscillationEvidence;
  /**
   * Restores the caller's ORIGINAL input object (identity), not a normalized
   * or re-cloned copy. The engine never mutated it, so it is intact.
   */
  rollback(): JsonValue;
}

export interface EngineOptions {
  /** Maximum number of rewrite steps before forced termination. Default 1000. */
  readonly budget?: number;
}

function collectCandidates(root: JsonValue, rules: readonly Rule[]): Candidate[] {
  const out: Candidate[] = [];
  for (const { path, value } of walk(root)) {
    const ctx: MatchContext = { root, path, value };
    for (const rule of rules) {
      if (rule.precondition(ctx)) {
        out.push({ rule, path, value });
      }
    }
  }
  return out;
}

function compareCandidates(a: Candidate, b: Candidate): number {
  if (a.rule.priority !== b.rule.priority) {
    return a.rule.priority - b.rule.priority;
  }
  const byPath = comparePaths(a.path, b.path);
  if (byPath !== 0) {
    return byPath;
  }
  return a.rule.name < b.rule.name ? -1 : a.rule.name > b.rule.name ? 1 : 0;
}

export function runRules(
  input: JsonValue,
  rules: readonly Rule[],
  options: EngineOptions = {},
): RunResult {
  const budget = options.budget ?? 1000;
  if (!Number.isInteger(budget) || budget < 0) {
    throw new RangeError(`budget must be a non-negative integer, got ${budget}`);
  }

  const original = input; // kept by identity for rollback; never mutated
  let working = deepClone(input);
  const steps: Step[] = [];
  /** canonical state hash -> step count at which that state occurred (0 = initial). */
  const seen = new Map<string, number>([[canonicalize(working), 0]]);

  let status: Status = 'budget-exhausted';
  let conflict: ConflictEvidence | undefined;
  let oscillation: OscillationEvidence | undefined;

  for (let seq = 1; seq <= budget; seq++) {
    const candidates = collectCandidates(working, rules);
    if (candidates.length === 0) {
      status = 'fixpoint';
      break;
    }
    candidates.sort(compareCandidates);
    const best = candidates[0];

    // Conflict: distinct rules, same priority, same path -> ambiguous, stop.
    const rivals = candidates.filter(
      (c) =>
        c.rule !== best.rule &&
        c.rule.priority === best.rule.priority &&
        samePath(c.path, best.path),
    );
    if (rivals.length > 0) {
      status = 'conflict';
      conflict = {
        path: formatPath(best.path),
        value: deepClone(best.value),
        competitors: [best, ...rivals].map((c) => ({
          rule: c.rule.name,
          priority: c.rule.priority,
        })),
      };
      break;
    }

    const ctx: MatchContext = { root: working, path: best.path, value: best.value };
    const next = best.rule.fix(best.value, ctx);
    steps.push({
      seq,
      rule: best.rule.name,
      priority: best.rule.priority,
      path: formatPath(best.path),
      segments: best.path,
      before: deepClone(best.value),
      after: deepClone(next),
    });
    working = setAt(working, best.path, next);

    const hash = canonicalize(working);
    const previousOccurrence = seen.get(hash);
    if (previousOccurrence !== undefined) {
      status = 'oscillation';
      const cycleSteps = steps.slice(previousOccurrence);
      oscillation = {
        stateHash: hash,
        cycleStartSeq: previousOccurrence + 1,
        cycleSteps,
        rulesInvolved: [...new Set(cycleSteps.map((s) => s.rule))],
      };
      break;
    }
    seen.set(hash, seq);
  }

  return {
    status,
    steps,
    output: working,
    conflict,
    oscillation,
    rollback: () => original,
  };
}
