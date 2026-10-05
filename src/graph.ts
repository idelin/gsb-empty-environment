/**
 * Labeled directed graph with a finite label set.
 * Edge ids are unique numbers; their numeric order defines the
 * tie-break order used by the query engine.
 */

export type NodeId = string;
export type EdgeId = number;
export type Label = string;

export interface Edge {
  readonly id: EdgeId;
  readonly from: NodeId;
  readonly to: NodeId;
  readonly label: Label;
}

export class Graph {
  private readonly edgeById = new Map<EdgeId, Edge>();
  private readonly outgoingMap = new Map<NodeId, Edge[]>();
  private readonly nodeSet = new Set<NodeId>();
  private readonly labelSet = new Set<Label>();

  constructor(edges: Iterable<Edge>) {
    for (const edge of edges) {
      if (this.edgeById.has(edge.id)) {
        throw new Error(`duplicate edge id: ${edge.id}`);
      }
      this.edgeById.set(edge.id, edge);
      this.nodeSet.add(edge.from);
      this.nodeSet.add(edge.to);
      this.labelSet.add(edge.label);
      let bucket = this.outgoingMap.get(edge.from);
      if (bucket === undefined) {
        bucket = [];
        this.outgoingMap.set(edge.from, bucket);
      }
      bucket.push(edge);
    }
    for (const bucket of this.outgoingMap.values()) {
      bucket.sort((a, b) => a.id - b.id);
    }
  }

  get nodes(): ReadonlySet<NodeId> {
    return this.nodeSet;
  }

  get labels(): ReadonlySet<Label> {
    return this.labelSet;
  }

  edge(id: EdgeId): Edge | undefined {
    return this.edgeById.get(id);
  }

  /** Outgoing edges of `node`, sorted by ascending edge id. */
  outgoing(node: NodeId): readonly Edge[] {
    return this.outgoingMap.get(node) ?? [];
  }
}
