import { initialState, statesEqual, cloneState, cloneSteps, type AbstractState } from './state.ts';
import { applyEdge, applyNode, edgeStep, nodeStep, violationKey, makeViolation, type ReportContext } from './transfer.ts';
import type {
  AnalysisResult,
  CfgEdge,
  CfgNode,
  PathStep,
  ProcedureGraph,
  Violation,
  ViolationPath,
} from './types.ts';

interface Branch {
  state: AbstractState;
  steps: PathStep[];
}

/**
 * 单调数据流分析器（worklist 不动点）。
 * 状态格有限（每变量 3 个状态的幂集 + 有限句柄/region），循环回边经有限次迭代收敛。
 * 违规按每个前驱分支独立检测，保证给出的见证路径是真实可执行路径；
 * 同一违规（violationKey）只保留第一条见证路径。
 */
export function analyze(graph: ProcedureGraph): AnalysisResult {
  const nodeById = new Map<string, CfgNode>(graph.nodes.map((n) => [n.id, n]));
  const incoming = new Map<string, CfgEdge[]>();
  const outgoing = new Map<string, CfgEdge[]>();
  for (const node of graph.nodes) {
    incoming.set(node.id, []);
    outgoing.set(node.id, []);
  }
  for (const edge of graph.edges) {
    incoming.get(edge.to)!.push(edge);
    outgoing.get(edge.from)!.push(edge);
  }

  const out = new Map<string, AbstractState>();
  const trace = new Map<string, PathStep[]>();
  const witnesses = new Map<string, ViolationPath>();

  const collect = (steps: PathStep[]): ReportContext => ({
    locationOf: (node: CfgNode) => node.id,
    report: (kind, location, extra) => {
      const violation = makeViolation(kind, location, extra);
      const key = violationKey(violation);
      if (!witnesses.has(key)) {
        witnesses.set(key, { violation, steps: cloneSteps(steps) });
      }
    },
  });

  const worklist: string[] = [graph.entry];
  const queued = new Set<string>([graph.entry]);
  let iterations = 0;

  while (worklist.length > 0) {
    const id = worklist.shift()!;
    queued.delete(id);
    iterations += 1;
    const node = nodeById.get(id)!;
    const branches: Branch[] = [];

    if (id === graph.entry) {
      branches.push({ state: initialState(), steps: [] });
    }
    for (const edge of incoming.get(id) ?? []) {
      const predOut = out.get(edge.from);
      const predTrace = trace.get(edge.from);
      if (!predOut || !predTrace) continue;
      const state = cloneState(predOut);
      const steps = cloneSteps(predTrace);
      steps.push(edgeStep(edge));
      applyEdge(state, edge, collect(steps));
      branches.push({ state, steps });
    }

    let joined: AbstractState | null = null;
    let canonicalTrace: PathStep[] | null = null;
    for (const branch of branches) {
      const state = cloneState(branch.state);
      const steps = cloneSteps(branch.steps);
      steps.push(nodeStep(id));
      applyNode(state, node, collect(steps));
      joined = joined === null ? state : joinInto(joined, state);
      if (canonicalTrace === null) canonicalTrace = steps;
    }
    const newOut = joined ?? initialState();
    const newTrace = canonicalTrace ?? [nodeStep(id)];

    const oldOut = out.get(id);
    if (!oldOut || !statesEqual(oldOut, newOut)) {
      out.set(id, newOut);
      trace.set(id, newTrace);
      for (const edge of outgoing.get(id) ?? []) {
        if (!queued.has(edge.to)) {
          queued.add(edge.to);
          worklist.push(edge.to);
        }
      }
    }
  }

  const facts: Record<string, unknown> = {};
  for (const node of graph.nodes) {
    const state = out.get(node.id);
    if (state) facts[node.id] = summarize(state);
  }

  return { violations: [...witnesses.values()], facts, iterations };
}

function joinInto(target: AbstractState, other: AbstractState): AbstractState {
  for (const [variable, statuses] of other.status) {
    const existing = target.status.get(variable);
    if (existing) for (const status of statuses) existing.add(status);
    else target.status.set(variable, new Set(statuses));
  }
  for (const [handle, vars] of other.borrows) {
    const existing = target.borrows.get(handle);
    if (existing) for (const variable of vars) existing.add(variable);
    else target.borrows.set(handle, new Set(vars));
  }
  return target;
}

function summarize(state: AbstractState): unknown {
  return {
    status: Object.fromEntries([...state.status].map(([k, v]) => [k, [...v].sort()])),
    borrows: Object.fromEntries([...state.borrows].map(([k, v]) => [k, [...v].sort()])),
    regionStack: [...state.regionStack],
    pending: Object.fromEntries([...state.pending].map(([k, v]) => [k, [...v]])),
  };
}
