// 用无共享朴素解释器核对 DAG 重写器：同一输入、同一预算，
// 要求状态、每一步（规则 / 路径 / 绑定 / 结果）完全一致。

import { Factory, Node, pretty } from "./terms";
import { Rewriter, Rule, State, TraceStep } from "./rewriter";
import { fromNode, naiveRun } from "./naive";

export interface CheckResult {
  ok: boolean;
  dagSteps: number;
  naiveSteps: number;
  status: string;
  final: string;
  mismatch?: string;
}

function sameStep(a: TraceStep, b: TraceStep): string | null {
  if (a.rule !== b.rule) return `rule: dag=${a.rule} naive=${b.rule}`;
  if (a.path.join(",") !== b.path.join(","))
    return `path: dag=[${a.path}] naive=[${b.path}]`;
  if (a.redex !== b.redex) return `redex: dag=${a.redex} naive=${b.redex}`;
  if (a.replacement !== b.replacement)
    return `replacement: dag=${a.replacement} naive=${b.replacement}`;
  const ka = Object.keys(a.bindings).sort();
  const kb = Object.keys(b.bindings).sort();
  if (ka.join(",") !== kb.join(","))
    return `binding vars: dag={${ka}} naive={${kb}}`;
  for (const k of ka) {
    if (a.bindings[k] !== b.bindings[k])
      return `binding ${k}: dag=${a.bindings[k]} naive=${b.bindings[k]}`;
  }
  return null;
}

// chunkSize > 0 时，DAG 端按该步数分段运行（顺带验证预算中断/恢复的正确性）。
export function crossCheck(
  factory: Factory,
  rules: readonly Rule[],
  root: Node,
  budget: number,
  chunkSize = 0,
): CheckResult {
  const rewriter = new Rewriter(factory, rules);

  // ---- DAG 端（可分段恢复）----
  let state: State = rewriter.init(root);
  const dagSteps: TraceStep[] = [];
  let remaining = budget;
  while (state.status === "running" || state.status === "budget") {
    const slice = chunkSize > 0 ? Math.min(chunkSize, remaining) : remaining;
    if (slice <= 0) break;
    const out = rewriter.run(state, slice);
    dagSteps.push(...out.steps);
    state = out.state;
    remaining -= out.steps.length;
    if (out.steps.length === 0) break; // normal-form 或 cycle
  }

  // ---- 朴素端 ----
  const naive = naiveRun(rules, fromNode(root), budget);

  // ---- 逐步核对 ----
  const fail = (mismatch: string): CheckResult => ({
    ok: false,
    dagSteps: dagSteps.length,
    naiveSteps: naive.steps.length,
    status: `dag=${state.status} naive=${naive.status}`,
    final: pretty(state.root),
    mismatch,
  });

  if (state.status !== naive.status) {
    return fail(`status: dag=${state.status} naive=${naive.status}`);
  }
  if (dagSteps.length !== naive.steps.length) {
    return fail(`step count: dag=${dagSteps.length} naive=${naive.steps.length}`);
  }
  for (let i = 0; i < dagSteps.length; i++) {
    const diff = sameStep(dagSteps[i], naive.steps[i]);
    if (diff) return fail(`step ${i}: ${diff}`);
  }
  if (pretty(state.root) !== naive.final) {
    return fail(`final: dag=${pretty(state.root)} naive=${naive.final}`);
  }
  const dagCycle = state.cycle ? `${state.cycle.from}->${state.cycle.to}` : "none";
  const naiveCycle = naive.cycle ? `${naive.cycle.from}->${naive.cycle.to}` : "none";
  if (dagCycle !== naiveCycle) {
    return fail(`cycle: dag=${dagCycle} naive=${naiveCycle}`);
  }
  return {
    ok: true,
    dagSteps: dagSteps.length,
    naiveSteps: naive.steps.length,
    status: state.status,
    final: pretty(state.root),
  };
}
