import { type FlowModel, type FlowNode, getNode, successors } from './model.ts';

/**
 * Structural path enumeration for finite (DAG) control-flow models.
 *
 * Enumeration is purely structural; feasibility against abstract state is
 * decided later by the verifier. Shared sub-graphs are expanded per path
 * while node-level memoisation keeps the walk linear in node count.
 */
export interface FlowPath {
  readonly nodes: readonly FlowNode[];
}

export function enumeratePaths(model: FlowModel): FlowPath[] {
  const memo = new Map<string, FlowNode[][]>();
  const onStack = new Set<string>();

  const walk = (nodeId: string): FlowNode[][] => {
    const cached = memo.get(nodeId);
    if (cached !== undefined) {
      return cached;
    }
    if (onStack.has(nodeId)) {
      throw new Error(
        `non-finite control flow in ${model.name}: back edge to "${nodeId}" (models must be DAGs)`,
      );
    }
    onStack.add(nodeId);
    const node = getNode(model, nodeId);
    const outs = successors(node);
    let result: FlowNode[][];
    if (outs.length === 0) {
      result = [[node]];
    } else {
      result = [];
      for (const successor of outs) {
        for (const suffix of walk(successor)) {
          result.push([node, ...suffix]);
        }
      }
    }
    onStack.delete(nodeId);
    memo.set(nodeId, result);
    return result;
  };

  return walk(model.entry).map((nodes) => ({ nodes }));
}
