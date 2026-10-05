// 朴素解释器：普通树、无任何共享，每次重写都做深拷贝。
// 它与 DAG 重写器独立实现同一套语义（优先级 + 最左最内层），
// 用来逐步核对 DAG 版本的重写序列。

import { Node, Pattern } from "./terms";
import { Rule, TraceStep } from "./rewriter";

export interface NTree {
  sym: string;
  args: NTree[];
}

// 把（可能共享的）DAG 展开成普通树。
export function fromNode(node: Node): NTree {
  return { sym: node.sym, args: node.args.map(fromNode) };
}

export function npretty(t: NTree): string {
  if (t.args.length === 0) return t.sym;
  return `${t.sym}(${t.args.map(npretty).join(", ")})`;
}

function deepCopy(t: NTree): NTree {
  return { sym: t.sym, args: t.args.map(deepCopy) };
}

function structuralEqual(a: NTree, b: NTree): boolean {
  if (a.sym !== b.sym || a.args.length !== b.args.length) return false;
  return a.args.every((c, i) => structuralEqual(c, b.args[i]));
}

type NBindings = Map<string, NTree>;

function nmatch(pat: Pattern, t: NTree, env: NBindings): NBindings | null {
  if (pat.kind === "var") {
    const prev = env.get(pat.name);
    if (prev === undefined) {
      env.set(pat.name, deepCopy(t));
      return env;
    }
    return structuralEqual(prev, t) ? env : null;
  }
  if (pat.sym !== t.sym || pat.args.length !== t.args.length) return null;
  for (let i = 0; i < pat.args.length; i++) {
    if (!nmatch(pat.args[i], t.args[i], env)) return null;
  }
  return env;
}

function ninstantiate(tmpl: Pattern, env: NBindings): NTree {
  if (tmpl.kind === "var") {
    const bound = env.get(tmpl.name);
    if (!bound) throw new Error(`unbound variable in template: ${tmpl.name}`);
    return deepCopy(bound); // 朴素语义：每次都深拷贝
  }
  return { sym: tmpl.sym, args: tmpl.args.map((a) => ninstantiate(a, env)) };
}

interface NRedex {
  path: number[];
  node: NTree;
  rule: Rule;
  bindings: NBindings;
}

export interface NaiveResult {
  status: "normal-form" | "cycle" | "budget";
  steps: TraceStep[];
  final: string;
  cycle?: { from: number; to: number };
}

export function naiveRun(rules: readonly Rule[], input: NTree, budget: number): NaiveResult {
  const ordered = rules
    .map((rule, index) => ({ rule, index }))
    .sort((a, b) => a.rule.priority - b.rule.priority || a.index - b.index)
    .map((e) => e.rule);

  const findRedex = (t: NTree, path: number[]): NRedex | null => {
    for (let i = 0; i < t.args.length; i++) {
      const hit = findRedex(t.args[i], [...path, i]);
      if (hit) return hit;
    }
    for (const rule of ordered) {
      const bindings = nmatch(rule.lhs, t, new Map());
      if (bindings) return { path, node: t, rule, bindings };
    }
    return null;
  };

  const replaceAt = (t: NTree, path: readonly number[], replacement: NTree): NTree => {
    if (path.length === 0) return replacement;
    const args = t.args.map(deepCopy); // 全树深拷贝，绝不共享
    args[path[0]] = replaceAt(t.args[path[0]], path.slice(1), replacement);
    return { sym: t.sym, args };
  };

  const seen = new Map<string, number>(); // 状态全文 -> 首次出现步数
  const steps: TraceStep[] = [];
  let root = input;
  let step = 0;

  while (budget < 0 || steps.length < budget) {
    const key = npretty(root);
    const previous = seen.get(key);
    if (previous !== undefined) {
      return { status: "cycle", steps, final: key, cycle: { from: previous, to: step } };
    }
    seen.set(key, step);

    const redex = findRedex(root, []);
    if (!redex) {
      return { status: "normal-form", steps, final: key };
    }

    const replacement = ninstantiate(redex.rule.rhs, redex.bindings);
    const bindings: Record<string, string> = {};
    for (const [name, value] of redex.bindings) bindings[name] = npretty(value);
    steps.push({
      step,
      path: redex.path,
      rule: redex.rule.name,
      redex: npretty(redex.node),
      bindings,
      replacement: npretty(replacement),
    });

    root = replaceAt(root, redex.path, replacement);
    step++;
  }
  return { status: "budget", steps, final: npretty(root) };
}
