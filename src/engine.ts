import { TermFactory, canon, termToJSON, termFromJSON } from "./term.ts";
import type { Term, Pat, TermJSON } from "./term.ts";
import { matchPattern, instantiate } from "./match.ts";
import type { Bindings } from "./match.ts";

export interface Rule {
  readonly name: string;
  readonly lhs: Pat;
  readonly rhs: Pat;
  /** Higher priority rules are tried first; ties keep declaration order. */
  readonly priority?: number;
}

export interface StepRecord {
  readonly step: number;
  /** Path from the root to the matched node, as child indices. */
  readonly path: readonly number[];
  readonly rule: string;
  /** Variable bindings at the hit, sorted by variable name. */
  readonly bindings: ReadonlyArray<readonly [string, Term]>;
  /** The matched node (redex) before rewriting. */
  readonly redex: Term;
  /** The whole term after this rewrite step. */
  readonly result: Term;
}

export type Status = "normal-form" | "cycle" | "budget";

export interface CycleInfo {
  /** Step index at which this state was first reached (0 = initial term). */
  readonly firstStep: number;
  /** Step index whose rewrite produced the repeated state. */
  readonly repeatedAt: number;
  readonly state: string;
}

export interface RunResult {
  readonly status: Status;
  /** Total number of rewrite steps executed so far. */
  readonly steps: number;
  readonly term: Term;
  readonly cycle?: CycleInfo;
}

interface Redex {
  readonly path: readonly number[];
  readonly rule: Rule;
  readonly bindings: Bindings;
}

/** Replace the subterm at `path` by `sub`, rebuilding only the spine. */
export function replaceAt(
  t: Term,
  path: readonly number[],
  sub: Term,
  fac: TermFactory,
): Term {
  if (path.length === 0) return sub;
  const [head, ...rest] = path;
  const args = t.args.slice();
  args[head] = replaceAt(t.args[head], rest, sub, fac);
  return fac.mk(t.op, args);
}

/**
 * Leftmost-innermost rewrite engine.
 *
 * The engine is resumable: when a run exhausts its step budget it stops
 * with status "budget" and can be continued by calling run() again, or
 * serialized with snapshot() and restored later.
 */
export class Engine {
  private readonly rules: readonly Rule[];
  private term: Term;
  private readonly trace: StepRecord[] = [];
  /** Canonical state -> step index at which it was reached. */
  private readonly seen = new Map<string, number>();
  private cycleInfo: CycleInfo | undefined;
  private halted = false;
  private readonly fac: TermFactory;

  constructor(
    rules: readonly Rule[],
    initial: Term,
    fac: TermFactory,
  ) {
    this.fac = fac;
    this.rules = rules
      .map((r, i) => ({ r, i }))
      .sort((a, b) => (b.r.priority ?? 0) - (a.r.priority ?? 0) || a.i - b.i)
      .map(({ r }) => r);
    this.term = initial;
    this.seen.set(canon(initial), 0);
  }

  get currentTerm(): Term {
    return this.term;
  }

  get steps(): readonly StepRecord[] {
    return this.trace;
  }

  /** Find the leftmost-innermost redex: children left-to-right first, then the node itself. */
  private findRedex(t: Term, path: readonly number[]): Redex | null {
    for (let i = 0; i < t.args.length; i++) {
      const hit = this.findRedex(t.args[i], [...path, i]);
      if (hit) return hit;
    }
    for (const rule of this.rules) {
      const bindings = matchPattern(rule.lhs, t);
      if (bindings) return { path, rule, bindings };
    }
    return null;
  }

  /**
   * Run at most `budget` more rewrite steps.
   * Returns the status and the current term; call again to resume.
   */
  run(budget = Infinity): RunResult {
    if (this.halted) return this.result("normal-form");
    let remaining = budget;
    while (remaining > 0) {
      const redex = this.findRedex(this.term, []);
      if (!redex) {
        this.halted = true;
        return this.result("normal-form");
      }
      const before = this.term;
      const contracted = instantiate(redex.rule.rhs, redex.bindings, this.fac);
      const next = replaceAt(before, redex.path, contracted, this.fac);
      const stepIndex = this.trace.length + 1;
      this.term = next;
      this.trace.push({
        step: stepIndex,
        path: redex.path,
        rule: redex.rule.name,
        bindings: [...redex.bindings.entries()].sort((a, b) =>
          a[0] < b[0] ? -1 : 1,
        ),
        redex: redex.path.reduce<Term>((t, i) => t.args[i], before),
        result: next,
      });
      const key = canon(next);
      const first = this.seen.get(key);
      if (first !== undefined) {
        this.cycleInfo = { firstStep: first, repeatedAt: stepIndex, state: key };
        this.halted = true;
        return this.result("cycle");
      }
      this.seen.set(key, stepIndex);
      remaining--;
    }
    return this.result("budget");
  }

  private result(status: Status): RunResult {
    return {
      status,
      steps: this.trace.length,
      term: this.term,
      cycle: this.cycleInfo,
    };
  }

  // ---- snapshot / restore -------------------------------------------------

  snapshot(): string {
    return JSON.stringify({
      term: termToJSON(this.term),
      trace: this.trace.map((s) => ({
        step: s.step,
        path: s.path,
        rule: s.rule,
        bindings: s.bindings.map(
          ([name, t]) => [name, termToJSON(t)] as const,
        ),
        redex: termToJSON(s.redex),
        result: termToJSON(s.result),
      })),
      seen: [...this.seen.entries()],
      cycle: this.cycleInfo ?? null,
      halted: this.halted,
    });
  }

  static restore(
    json: string,
    rules: readonly Rule[],
    fac: TermFactory,
  ): Engine {
    const data = JSON.parse(json) as {
      term: TermJSON;
      trace: Array<{
        step: number;
        path: number[];
        rule: string;
        bindings: Array<readonly [string, TermJSON]>;
        redex: TermJSON;
        result: TermJSON;
      }>;
      seen: Array<[string, number]>;
      cycle: CycleInfo | null;
      halted: boolean;
    };
    const engine = new Engine(rules, termFromJSON(data.term, fac), fac);
    for (const s of data.trace) {
      engine.trace.push({
        step: s.step,
        path: s.path,
        rule: s.rule,
        bindings: s.bindings.map(
          ([name, t]) => [name, termFromJSON(t, fac)] as const,
        ),
        redex: termFromJSON(s.redex, fac),
        result: termFromJSON(s.result, fac),
      });
    }
    engine.seen.clear();
    for (const [k, idx] of data.seen) engine.seen.set(k, idx);
    engine.cycleInfo = data.cycle ?? undefined;
    engine.halted = data.halted;
    return engine;
  }
}
