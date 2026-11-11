import { initialState, cloneState, type AbstractState } from './state.ts';
import { applyEdge, applyNode, edgeStep, nodeStep, violationKey, makeViolation, type ReportContext } from './transfer.ts';
import type { CfgNode, PathStep, ProcedureGraph, Violation } from './types.ts';

export interface ConcreteSequence {
  /** 该执行序列经过的节点与异常边（含延迟清理）。 */
  steps: PathStep[];
  violations: Violation[];
  /** 因长度上限被截断（通常表示含循环）。 */
  truncated: boolean;
}

export interface EnumerationResult {
  sequences: ConcreteSequence[];
  /** 所有序列上出现过的违规键（violationKey 去重）。 */
  violationKeys: Set<string>;
  truncated: boolean;
}

/**
 * 小图具体解释器：DFS 枚举从入口到汇点的所有执行序列。
 * 与抽象分析共用同一份转移语义（applyNode/applyEdge），
 * 但具体状态始终为单值（无 join），用于交叉验证不动点分析。
 * bound 限制单条序列的节点步数，防止循环导致无限枚举。
 */
export function enumerateSequences(graph: ProcedureGraph, bound = 40): EnumerationResult {
  const nodeById = new Map<string, CfgNode>(graph.nodes.map((n) => [n.id, n]));
  const outgoing = new Map<string, (typeof graph.edges)[number][]>();
  for (const node of graph.nodes) outgoing.set(node.id, []);
  for (const edge of graph.edges) outgoing.get(edge.from)!.push(edge);

  const sequences: ConcreteSequence[] = [];
  const violationKeys = new Set<string>();
  let anyTruncated = false;

  const visit = (id: string, state: AbstractState, steps: PathStep[], violations: Violation[], depth: number): void => {
    const node = nodeById.get(id)!;
    const nodeSteps = [...steps, nodeStep(id)];
    const nodeState = cloneState(state);
    const ctx: ReportContext = {
      locationOf: (n: CfgNode) => n.id,
      report: (kind, location, extra) => {
        const violation = makeViolation(kind, location, extra);
        violations.push(violation);
        violationKeys.add(violationKey(violation));
      },
    };
    applyNode(nodeState, node, ctx);

    const edges = outgoing.get(id) ?? [];
    if (edges.length === 0) {
      sequences.push({ steps: nodeSteps, violations: [...violations], truncated: false });
      return;
    }
    if (depth + 1 > bound) {
      anyTruncated = true;
      sequences.push({ steps: nodeSteps, violations: [...violations], truncated: true });
      return;
    }

    for (const edge of edges) {
      const edgeState = cloneState(nodeState);
      const edgeSteps = [...nodeSteps, edgeStep(edge)];
      const edgeViolations = [...violations];
      const edgeCtx: ReportContext = {
        locationOf: (n: CfgNode) => n.id,
        report: (kind, location, extra) => {
          const violation = makeViolation(kind, location, extra);
          edgeViolations.push(violation);
          violationKeys.add(violationKey(violation));
        },
      };
      applyEdge(edgeState, edge, edgeCtx);
      visit(edge.to, edgeState, edgeSteps, edgeViolations, depth + 1);
    }
  };

  visit(graph.entry, initialState(), [], [], 0);
  return { sequences, violationKeys, truncated: anyTruncated };
}
