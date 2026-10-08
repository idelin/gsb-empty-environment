import type {
  AggregatedEdge,
  Graph,
  GraphEdge,
  GraphNode,
  VisibleGraph,
  VisibleNode,
} from "./types.ts";

/**
 * Preprocesses a hierarchical graph for layout: collapsed subtrees are mapped
 * onto their visible ancestor, cross-subtree edges are aggregated while keeping
 * the original edge ids, and edges internal to a collapsed subtree (which would
 * become self-loops) are hidden but counted.
 *
 * The visible graph is a pure function of the source graph plus the collapsed
 * set, so expanding a node restores the original edges by recomputation — the
 * library never stores inverse operations or guesses how to undo an aggregate.
 */
export class CollapsePreprocessor {
  private readonly nodes: GraphNode[];
  private readonly edges: GraphEdge[];
  private readonly parentOf = new Map<string, string | null>();
  private readonly collapsed = new Set<string>();

  constructor(graph: Graph) {
    this.nodes = graph.nodes.map((n) => ({ ...n }));
    this.edges = graph.edges.map((e) => ({ ...e }));

    const ids = new Set<string>();
    for (const node of this.nodes) {
      if (ids.has(node.id)) {
        throw new Error(`Duplicate node id: ${node.id}`);
      }
      ids.add(node.id);
    }
    for (const node of this.nodes) {
      const parent = node.parent ?? null;
      if (parent !== null && !ids.has(parent)) {
        throw new Error(`Node ${node.id} has unknown parent: ${parent}`);
      }
      this.parentOf.set(node.id, parent);
    }
    for (const id of ids) {
      this.assertAcyclic(id);
    }

    const edgeIds = new Set<string>();
    for (const edge of this.edges) {
      if (edgeIds.has(edge.id)) {
        throw new Error(`Duplicate edge id: ${edge.id}`);
      }
      edgeIds.add(edge.id);
      for (const endpoint of [edge.source, edge.target]) {
        if (!ids.has(endpoint)) {
          throw new Error(`Edge ${edge.id} references unknown node: ${endpoint}`);
        }
      }
    }
  }

  private assertAcyclic(id: string): void {
    const seen = new Set<string>([id]);
    let cursor = this.parentOf.get(id) ?? null;
    while (cursor !== null) {
      if (seen.has(cursor)) {
        throw new Error(`Cycle in parent chain of node: ${id}`);
      }
      seen.add(cursor);
      cursor = this.parentOf.get(cursor) ?? null;
    }
  }

  private requireNode(id: string): void {
    if (!this.parentOf.has(id)) {
      throw new Error(`Unknown node: ${id}`);
    }
  }

  collapse(id: string): void {
    this.requireNode(id);
    this.collapsed.add(id);
  }

  expand(id: string): void {
    this.requireNode(id);
    this.collapsed.delete(id);
  }

  toggle(id: string): void {
    this.requireNode(id);
    if (this.collapsed.has(id)) {
      this.collapsed.delete(id);
    } else {
      this.collapsed.add(id);
    }
  }

  /** Expands every collapsed node, fully restoring the original edges. */
  expandAll(): void {
    this.collapsed.clear();
  }

  isCollapsed(id: string): boolean {
    this.requireNode(id);
    return this.collapsed.has(id);
  }

  collapsedIds(): string[] {
    return [...this.collapsed];
  }

  /** A node is hidden iff some strict ancestor is collapsed. */
  isHidden(id: string): boolean {
    this.requireNode(id);
    let cursor = this.parentOf.get(id) ?? null;
    while (cursor !== null) {
      if (this.collapsed.has(cursor)) {
        return true;
      }
      cursor = this.parentOf.get(cursor) ?? null;
    }
    return false;
  }

  /**
   * The visible node that stands in for `id`: `id` itself when visible,
   * otherwise its outermost collapsed ancestor (the one not hidden by an even
   * higher collapse).
   */
  representative(id: string): string {
    this.requireNode(id);
    let cursor = id;
    while (this.isHidden(cursor)) {
      cursor = this.parentOf.get(cursor) as string;
    }
    return cursor;
  }

  /**
   * Recomputes the visible graph from scratch. Expansion is exact because the
   * source edges are never mutated: every original edge id is either present in
   * exactly one aggregated edge or listed as a hidden internal edge of exactly
   * one visible node.
   */
  visibleGraph(): VisibleGraph {
    const visibleNodes: VisibleNode[] = [];
    const visibleById = new Map<string, VisibleNode>();
    for (const node of this.nodes) {
      if (this.isHidden(node.id)) {
        continue;
      }
      const visible: VisibleNode = {
        id: node.id,
        collapsed: this.collapsed.has(node.id),
        hiddenDescendants: 0,
        hiddenEdgeCount: 0,
        hiddenEdgeIds: [],
      };
      visibleNodes.push(visible);
      visibleById.set(node.id, visible);
    }

    for (const node of this.nodes) {
      if (this.isHidden(node.id)) {
        const rep = visibleById.get(this.representative(node.id));
        if (rep) {
          rep.hiddenDescendants += 1;
        }
      }
    }

    const edges: AggregatedEdge[] = [];
    const aggregateByKey = new Map<string, AggregatedEdge>();
    for (const edge of this.edges) {
      const source = this.representative(edge.source);
      const target = this.representative(edge.target);
      const isGenuineSelfLoop =
        source === target && edge.source === source && edge.target === target;

      if (source === target && !isGenuineSelfLoop) {
        // Internal edge of a collapsed subtree: hidden from layout, but counted
        // on the visible representative with its original id retained.
        const host = visibleById.get(source);
        if (host) {
          host.hiddenEdgeCount += 1;
          host.hiddenEdgeIds.push(edge.id);
        }
        continue;
      }

      const key = `${source}→${target}`;
      let aggregate = aggregateByKey.get(key);
      if (!aggregate) {
        aggregate = { id: key, source, target, edgeIds: [], count: 0 };
        aggregateByKey.set(key, aggregate);
        edges.push(aggregate);
      }
      aggregate.edgeIds.push(edge.id);
      aggregate.count += 1;
    }

    return { nodes: visibleNodes, edges };
  }
}
