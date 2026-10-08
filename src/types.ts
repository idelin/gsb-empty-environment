/** A node in the source graph. `parent` defines the nesting hierarchy used for collapse. */
export interface GraphNode {
  id: string;
  parent?: string | null;
}

/** A directed edge in the source graph. Ids must be unique. */
export interface GraphEdge {
  id: string;
  source: string;
  target: string;
}

export interface Graph {
  nodes: GraphNode[];
  edges: GraphEdge[];
}

/** A node that survives collapsing and can be rendered/laid out. */
export interface VisibleNode {
  id: string;
  /** Whether this node is itself collapsed (its subtree is hidden behind it). */
  collapsed: boolean;
  /** Number of hidden descendant nodes behind this node (0 when not collapsed). */
  hiddenDescendants: number;
  /**
   * Internal edges of this node's collapsed subtree. Both endpoints map to this
   * node, so they would be self-loops; they are hidden from layout but counted
   * here, with the original edge ids retained for inspection.
   */
  hiddenEdgeCount: number;
  hiddenEdgeIds: string[];
}

/**
 * An edge of the visible graph. Parallel original edges between the same pair of
 * visible endpoints are aggregated into one edge; `edgeIds` preserves every
 * original edge id in source order so expansion never has to guess.
 */
export interface AggregatedEdge {
  /** Deterministic id derived from the visible endpoints: `source→target`. */
  id: string;
  source: string;
  target: string;
  /** Original edge ids folded into this aggregate, in source edge order. */
  edgeIds: string[];
  count: number;
}

export interface VisibleGraph {
  nodes: VisibleNode[];
  edges: AggregatedEdge[];
}
