/**
 * Shortest feasible tainted-path search.
 *
 * Breadth-first search over the product of (CFG node x data state x
 * condition state). BFS order guarantees the first dangerous sink reached
 * yields a path with the fewest edges. Guards on edges are checked against
 * the finite-domain condition state, so mutually contradictory branches
 * are pruned and never appear in the witness.
 */

import { CondState, Domains, cloneCondState, collectDomains, condStateKey, killVariable, refine } from "./conditions";
import { CfgEdge, Graph } from "./model";
import { Danger, DataState, applyTransfer, cloneState, dataStateKey, emptyState } from "./state";

export interface PathStep {
  nodeId: string;
  /** Guard of the edge taken to leave this node (undefined on the last step). */
  viaCond?: string;
  /** Rule-effect notes produced while executing this node. */
  notes: string[];
}

export interface TaintWitness {
  /** True when a dangerous sink was reached. */
  found: boolean;
  /** Ordered feasible path from entry to the dangerous sink. */
  path: PathStep[];
  /** Dangerous arguments at the sink with their tainting sources. */
  dangers: Danger[];
  /** Sink node id (when found). */
  sinkNodeId?: string;
  /** Number of product states explored. */
  explored: number;
  warnings: string[];
}

interface WorkItem {
  nodeId: string;
  data: DataState;
  cond: CondState;
  path: PathStep[];
}

export interface AnalyzeOptions {
  /** Safety bound on explored product states. Default 100_000. */
  maxStates?: number;
}

export function analyze(graph: Graph, options: AnalyzeOptions = {}): TaintWitness {
  const maxStates = options.maxStates ?? 100_000;
  const domains: Domains = collectDomains(graph.edges.map((e: CfgEdge) => e.cond));
  const warnings: string[] = [];

  const initial: WorkItem = {
    nodeId: graph.entry,
    data: emptyState(),
    cond: new Map(),
    path: [],
  };
  const queue: WorkItem[] = [initial];
  const visited = new Set<string>();
  visited.add(`${graph.entry}|${dataStateKey(initial.data)}|${condStateKey(initial.cond)}`);
  let explored = 0;

  while (queue.length > 0) {
    const item = queue.shift() as WorkItem;
    const node = graph.nodeById.get(item.nodeId);
    if (!node) continue;

    const data = cloneState(item.data);
    const result = applyTransfer(graph, node, data);
    warnings.push(...result.warnings);

    let cond: CondState = item.cond;
    for (const variable of result.killed) {
      cond = killVariable(cond, variable);
    }

    const step: PathStep = { nodeId: item.nodeId, notes: result.notes };
    const path = [...item.path, step];
    explored++;

    if (result.dangers.length > 0) {
      return { found: true, path, dangers: result.dangers, sinkNodeId: item.nodeId, explored, warnings };
    }

    const outEdges = graph.outEdges.get(item.nodeId) ?? [];
    for (const edge of outEdges) {
      const refined = refine(cond, domains, edge.cond);
      if (refined === null) continue; // contradictory branch: infeasible
      const key = `${edge.to}|${dataStateKey(data)}|${condStateKey(refined)}`;
      if (visited.has(key)) continue;
      visited.add(key);
      const nextPath = path.slice();
      const last = nextPath[nextPath.length - 1] as PathStep;
      if (edge.cond !== undefined) {
        nextPath[nextPath.length - 1] = { ...last, viaCond: edge.cond };
      }
      queue.push({
        nodeId: edge.to,
        data: cloneState(data),
        cond: cloneCondState(refined),
        path: nextPath,
      });
    }

    if (explored > maxStates) {
      throw new Error(`analysis aborted: explored more than ${maxStates} states (possible unbounded loop)`);
    }
  }

  return { found: false, path: [], dangers: [], explored, warnings };
}
