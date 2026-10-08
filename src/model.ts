import type { Graph, VisibleGraph } from "./types.ts";

/**
 * Reference model: the literal per-edge mapping semantics, written as plainly
 * as possible and independent of CollapsePreprocessor's internals. Tests
 * cross-check the preprocessor against this model on nested collapses,
 * parallel edges, and collapse/expand sequences.
 *
 * Semantics, edge by edge:
 *   rep(n)  = n if no strict ancestor of n is collapsed, else the outermost
 *             collapsed ancestor of n.
 *   edge e  = (rep(e.source) → rep(e.target)); if both endpoints land on the
 *             same visible node and e is not already a self-loop on that node,
 *             e is an internal edge: hidden, but counted on that node.
 */
export function mapEdgesPerEdge(graph: Graph, collapsedIds: Iterable<string>): VisibleGraph {
  const collapsed = new Set(collapsedIds);
  const parentOf = new Map<string, string | null>();
  for (const node of graph.nodes) {
    parentOf.set(node.id, node.parent ?? null);
  }

  const isHidden = (id: string): boolean => {
    let cursor = parentOf.get(id) ?? null;
    while (cursor !== null) {
      if (collapsed.has(cursor)) {
        return true;
      }
      cursor = parentOf.get(cursor) ?? null;
    }
    return false;
  };

  const rep = (id: string): string => {
    let cursor = id;
    while (isHidden(cursor)) {
      cursor = parentOf.get(cursor) as string;
    }
    return cursor;
  };

  const nodeOrder: string[] = [];
  const nodeInfo: Record<string, {
    collapsed: boolean;
    hiddenDescendants: number;
    hiddenEdgeIds: string[];
  }> = {};
  for (const node of graph.nodes) {
    if (isHidden(node.id)) {
      continue;
    }
    nodeOrder.push(node.id);
    nodeInfo[node.id] = {
      collapsed: collapsed.has(node.id),
      hiddenDescendants: 0,
      hiddenEdgeIds: [],
    };
  }
  for (const node of graph.nodes) {
    if (isHidden(node.id)) {
      const host = nodeInfo[rep(node.id)];
      if (host) {
        host.hiddenDescendants += 1;
      }
    }
  }

  const edgeOrder: string[] = [];
  const edgeInfo: Record<string, { source: string; target: string; edgeIds: string[] }> = {};
  for (const edge of graph.edges) {
    const source = rep(edge.source);
    const target = rep(edge.target);
    const untouched = edge.source === source && edge.target === target;
    if (source === target && !untouched) {
      const host = nodeInfo[source];
      if (host) {
        host.hiddenEdgeIds.push(edge.id);
      }
      continue;
    }
    const key = `${source}→${target}`;
    const existing = edgeInfo[key];
    if (existing) {
      existing.edgeIds.push(edge.id);
    } else {
      edgeInfo[key] = { source, target, edgeIds: [edge.id] };
      edgeOrder.push(key);
    }
  }

  return {
    nodes: nodeOrder.map((id) => {
      const info = nodeInfo[id] as (typeof nodeInfo)[string];
      return {
        id,
        collapsed: info.collapsed,
        hiddenDescendants: info.hiddenDescendants,
        hiddenEdgeCount: info.hiddenEdgeIds.length,
        hiddenEdgeIds: info.hiddenEdgeIds,
      };
    }),
    edges: edgeOrder.map((key) => {
      const info = edgeInfo[key] as (typeof edgeInfo)[string];
      return {
        id: key,
        source: info.source,
        target: info.target,
        edgeIds: info.edgeIds,
        count: info.edgeIds.length,
      };
    }),
  };
}
