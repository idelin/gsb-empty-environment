import { TermFactory, canon, termToJSON, termFromJSON } from "./term.ts";
import type { Term, TermJSON } from "./term.ts";
import { Engine } from "./engine.ts";
import type { Rule, Status, CycleInfo } from "./engine.ts";
import { runNaive, ncanon } from "./naive.ts";
import type { NTree } from "./naive.ts";

/** Comparable (sharing-agnostic) view of one rewrite step. */
export interface TraceLine {
  step: number;
  path: number[];
  rule: string;
  bindings: Array<[string, string]>;
  redex: string;
  result: string;
}

export interface ComparableRun {
  status: Status;
  steps: number;
  finalTerm: string;
  cycle?: CycleInfo;
  trace: TraceLine[];
}

/** Run the hash-consed engine to completion (or cycle). */
export function runEngine(
  rules: readonly Rule[],
  initial: Term,
  fac: TermFactory,
  budget = Infinity,
): ComparableRun {
  const engine = new Engine(rules, initial, fac);
  const res = engine.run(budget);
  return {
    status: res.status,
    steps: res.steps,
    finalTerm: canon(res.term),
    cycle: res.cycle,
    trace: engine.steps.map((s) => ({
      step: s.step,
      path: [...s.path],
      rule: s.rule,
      bindings: s.bindings.map(([k, t]) => [k, canon(t)] as [string, string]),
      redex: canon(s.redex),
      result: canon(s.result),
    })),
  };
}

/** Run the naive interpreter to completion (or cycle). */
export function runNaiveComparable(
  rules: readonly Rule[],
  initial: NTree,
  budget = Infinity,
): ComparableRun {
  const res = runNaive(rules, initial, budget);
  return {
    status: res.status,
    steps: res.steps,
    finalTerm: ncanon(res.term),
    cycle: res.cycle,
    trace: res.trace,
  };
}

/** Convert a naive tree into a hash-consed term. */
export function toTerm(t: NTree, fac: TermFactory): Term {
  return termFromJSON(t as TermJSON, fac);
}

/** Convert a hash-consed term into a naive tree. */
export function toNTree(t: Term): NTree {
  return termToJSON(t) as NTree;
}

function diff(a: ComparableRun, b: ComparableRun): string | null {
  if (a.status !== b.status) return `status: engine=${a.status} naive=${b.status}`;
  if (a.steps !== b.steps) return `steps: engine=${a.steps} naive=${b.steps}`;
  if (a.finalTerm !== b.finalTerm)
    return `final term: engine=${a.finalTerm} naive=${b.finalTerm}`;
  const ca = a.cycle ? `${a.cycle.firstStep}->${a.cycle.repeatedAt}` : "none";
  const cb = b.cycle ? `${b.cycle.firstStep}->${b.cycle.repeatedAt}` : "none";
  if (ca !== cb) return `cycle: engine=${ca} naive=${cb}`;
  for (let i = 0; i < a.trace.length; i++) {
    const x = a.trace[i];
    const y = b.trace[i];
    const jx = JSON.stringify(x);
    const jy = JSON.stringify(y);
    if (jx !== jy) return `step ${i + 1}:\n  engine: ${jx}\n  naive:  ${jy}`;
  }
  return null;
}

/**
 * Cross-check the hash-consed engine against the naive no-sharing
 * interpreter on the same rules and initial term. Throws on divergence.
 */
export function crossCheck(
  label: string,
  rules: readonly Rule[],
  initial: NTree,
  budget = Infinity,
): ComparableRun {
  const fac = new TermFactory();
  const engineRun = runEngine(rules, toTerm(initial, fac), fac, budget);
  const naiveRun = runNaiveComparable(rules, initial, budget);
  const d = diff(engineRun, naiveRun);
  if (d) {
    throw new Error(`cross-check failed [${label}]:\n${d}`);
  }
  return engineRun;
}
