import type { Graph } from "./graph.ts";

export interface ViewNode {
  id: string;
  /** True when this visible node is a collapsed subtree root. */
  collapsed: boolean;
  /** Descendants hidden because this node is collapsed (0 when not collapsed). */
  hiddenNodeCount: number;
  /** Original edges fully inside this collapsed subtree (0 when not collapsed). */
  hiddenEdgeCount: number;
}

export interface AggregatedEdge {
  /** Visible endpoint after mapping the original edge endpoints. */
  source: string;
  target: string;
  /** Original edge ids, in graph insertion order. Never empty. */
  edgeIds: string[];
}

export interface ViewSnapshot {
  nodes: ViewNode[];
  edges: AggregatedEdge[];
}

/**
 * Collapse/expand state over an immutable original graph.
 *
 * The view is always derived by mapping every original edge through
 * `visibleAncestorOf`. Expanding never reconstructs edges by guessing
 * how an aggregate was formed; it simply re-derives the view, so the
 * original edges reappear exactly.
 */
export class CollapseView {
  private readonly collapsed = new Set<string>();
  private readonly graph: Graph;

  constructor(graph: Graph) {
    this.graph = graph;
  }

  collapse(id: string): void {
    this.graph.getNode(id);
    this.collapsed.add(id);
  }

  expand(id: string): void {
    this.graph.getNode(id);
    this.collapsed.delete(id);
  }

  toggle(id: string): void {
    if (this.collapsed.has(id)) {
      this.expand(id);
    } else {
      this.collapse(id);
    }
  }

  isCollapsed(id: string): boolean {
    return this.collapsed.has(id);
  }

  /** True when a strict ancestor of `id` is collapsed (the node itself is not shown). */
  isHidden(id: string): boolean {
    let current = this.graph.parentOf(id);
    while (current !== null) {
      if (this.collapsed.has(current)) {
        return true;
      }
      current = this.graph.parentOf(current);
    }
    return false;
  }

  isVisible(id: string): boolean {
    return !this.isHidden(id);
  }

  /**
   * The node that stands in for `id` in the current view: `id` itself when
   * visible, otherwise its nearest visible ancestor (always a collapsed node).
   */
  visibleAncestorOf(id: string): string {
    let current = id;
    while (this.isHidden(current)) {
      const parent = this.graph.parentOf(current);
      if (parent === null) {
        throw new Error(`node ${id} has no visible ancestor`);
      }
      current = parent;
    }
    return current;
  }

  /**
   * Derive the current view from the original edges, one by one:
   * - map each endpoint to its visible ancestor;
   * - when both endpoints land on the same collapsed node, the edge is
   *   internal to that subtree: hidden, counted on that node;
   * - otherwise the edge stays visible and is aggregated by directed
   *   (source, target) pair, keeping every original edge id.
   */
  getView(): ViewSnapshot {
    const nodes: ViewNode[] = [];
    const nodeById = new Map<string, ViewNode>();
    for (const id of this.graph.nodeIds()) {
      if (this.isVisible(id)) {
        const viewNode: ViewNode = {
          id,
          collapsed: this.collapsed.has(id),
          hiddenNodeCount: 0,
          hiddenEdgeCount: 0,
        };
        nodes.push(viewNode);
        nodeById.set(id, viewNode);
      }
    }

    for (const id of this.graph.nodeIds()) {
      if (this.isHidden(id)) {
        nodeById.get(this.visibleAncestorOf(id))!.hiddenNodeCount += 1;
      }
    }

    const edges: AggregatedEdge[] = [];
    const edgeByKey = new Map<string, AggregatedEdge>();
    for (const edgeId of this.graph.edgeIds()) {
      const edge = this.graph.getEdge(edgeId);
      const source = this.visibleAncestorOf(edge.source);
      const target = this.visibleAncestorOf(edge.target);
      if (source === target && this.collapsed.has(source)) {
        nodeById.get(source)!.hiddenEdgeCount += 1;
        continue;
      }
      const key = JSON.stringify([source, target]);
      const existing = edgeByKey.get(key);
      if (existing) {
        existing.edgeIds.push(edgeId);
      } else {
        const aggregated: AggregatedEdge = { source, target, edgeIds: [edgeId] };
        edgeByKey.set(key, aggregated);
        edges.push(aggregated);
      }
    }

    return { nodes, edges };
  }
}
