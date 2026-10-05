import type { Pat } from "./term.ts";
import type { Rule, Status, CycleInfo } from "./engine.ts";

/**
 * Naive tree interpreter used to cross-check the hash-consed engine.
 *
 * Terms are plain mutable-shaped objects with NO sharing: every
 * substitution and every rewrite step deep-copies. If the engine's
 * sharing were to alias-mutate or mis-copy, the traces would diverge.
 */

export interface NTree {
  op: string;
  args: NTree[];
}

export function ncanon(t: NTree): string {
  if (t.args.length === 0) return t.op;
  return `${t.op}(${t.args.map(ncanon).join(",")})`;
}

function nEq(a: NTree, b: NTree): boolean {
  return ncanon(a) === ncanon(b);
}

function clone(t: NTree): NTree {
  return { op: t.op, args: t.args.map(clone) };
}

function nMatch(pat: Pat, t: NTree, bind: Map<string, NTree>): boolean {
  if (pat.kind === "var") {
    const prev = bind.get(pat.name);
    if (!prev) {
      bind.set(pat.name, clone(t)); // logical copy, never aliased
      return true;
    }
    return nEq(prev, t);
  }
  if (pat.op !== t.op || pat.args.length !== t.args.length) return false;
  for (let i = 0; i < pat.args.length; i++) {
    if (!nMatch(pat.args[i], t.args[i], bind)) return false;
  }
  return true;
}

function nInstantiate(tpl: Pat, bind: Map<string, NTree>): NTree {
  if (tpl.kind === "var") {
    const t = bind.get(tpl.name);
    if (!t) throw new Error(`unbound variable in template: ${tpl.name}`);
    return clone(t); // deep copy: no sharing anywhere
  }
  return { op: tpl.op, args: tpl.args.map((a) => nInstantiate(a, bind)) };
}

function nReplaceAt(t: NTree, path: readonly number[], sub: NTree): NTree {
  const out = clone(t);
  let cur = out;
  for (let i = 0; i < path.length - 1; i++) cur = cur.args[path[i]];
  if (path.length === 0) return sub;
  cur.args[path[path.length - 1]] = sub;
  return out;
}

export interface NStep {
  step: number;
  path: number[];
  rule: string;
  bindings: Array<[string, string]>; // name -> canonical image
  redex: string;
  result: string;
}

export interface NRunResult {
  status: Status;
  steps: number;
  term: NTree;
  trace: NStep[];
  cycle?: CycleInfo;
}

interface NRedex {
  path: number[];
  rule: Rule;
  bindings: Map<string, NTree>;
}

export function runNaive(
  rules: readonly Rule[],
  initial: NTree,
  budget = Infinity,
): NRunResult {
  const ordered = rules
    .map((r, i) => ({ r, i }))
    .sort((a, b) => (b.r.priority ?? 0) - (a.r.priority ?? 0) || a.i - b.i)
    .map(({ r }) => r);

  const findRedex = (t: NTree, path: number[]): NRedex | null => {
    for (let i = 0; i < t.args.length; i++) {
      const hit = findRedex(t.args[i], [...path, i]);
      if (hit) return hit;
    }
    for (const rule of ordered) {
      const bind = new Map<string, NTree>();
      if (nMatch(rule.lhs, t, bind)) return { path, rule, bindings: bind };
    }
    return null;
  };

  let term = clone(initial);
  const trace: NStep[] = [];
  const seen = new Map<string, number>([[ncanon(term), 0]]);

  while (trace.length < budget) {
    const redex = findRedex(term, []);
    if (!redex) return { status: "normal-form", steps: trace.length, term, trace };
    const redexNode = redex.path.reduce<NTree>((t, i) => t.args[i], term);
    const contracted = nInstantiate(redex.rule.rhs, redex.bindings);
    term = nReplaceAt(term, redex.path, contracted);
    trace.push({
      step: trace.length + 1,
      path: redex.path,
      rule: redex.rule.name,
      bindings: [...redex.bindings.entries()]
        .map(([k, v]) => [k, ncanon(v)] as [string, string])
        .sort((a, b) => (a[0] < b[0] ? -1 : 1)),
      redex: ncanon(redexNode),
      result: ncanon(term),
    });
    const key = ncanon(term);
    const first = seen.get(key);
    if (first !== undefined) {
      return {
        status: "cycle",
        steps: trace.length,
        term,
        trace,
        cycle: { firstStep: first, repeatedAt: trace.length, state: key },
      };
    }
    seen.set(key, trace.length);
  }
  return { status: "budget", steps: trace.length, term, trace };
}
