import { Edge, EdgeId, GraphSpec, NodeId } from "./types";

/** Labeled directed multigraph with unique edge ids and a finite label set. */
export class Graph {
  readonly nodeCount: number;
  private readonly edgeById = new Map<EdgeId, Edge>();
  private readonly adjacency = new Map<NodeId, Edge[]>();
  private readonly labelSet = new Set<string>();

  constructor(spec: GraphSpec) {
    if (!Number.isInteger(spec.nodeCount) || spec.nodeCount < 0) {
      throw new Error("nodeCount must be a non-negative integer");
    }
    this.nodeCount = spec.nodeCount;
    for (const edge of spec.edges) {
      if (!Number.isInteger(edge.id)) {
        throw new Error("edge id must be an integer: " + edge.id);
      }
      if (this.edgeById.has(edge.id)) {
        throw new Error("duplicate edge id: " + edge.id);
      }
      for (const endpoint of [edge.from, edge.to]) {
        if (!Number.isInteger(endpoint) || endpoint < 0 || endpoint >= spec.nodeCount) {
          throw new Error("edge " + edge.id + " has endpoint out of range: " + endpoint);
        }
      }
      const frozen: Edge = { id: edge.id, from: edge.from, to: edge.to, label: edge.label };
      this.edgeById.set(edge.id, frozen);
      this.labelSet.add(edge.label);
      let list = this.adjacency.get(edge.from);
      if (list === undefined) {
        list = [];
        this.adjacency.set(edge.from, list);
      }
      list.push(frozen);
    }
    for (const list of this.adjacency.values()) {
      list.sort((a, b) => a.id - b.id);
    }
  }

  edge(id: EdgeId): Edge | undefined {
    return this.edgeById.get(id);
  }

  hasEdge(id: EdgeId): boolean {
    return this.edgeById.has(id);
  }

  labels(): string[] {
    return Array.from(this.labelSet).sort();
  }

  /** All edges sorted by id. */
  allEdges(): Edge[] {
    return Array.from(this.edgeById.values()).sort((a, b) => a.id - b.id);
  }

  /** Outgoing edges of `node` sorted by edge id, excluding disabled ones. */
  outgoingFrom(node: NodeId, disabledEdges?: ReadonlySet<EdgeId>): Edge[] {
    const list = this.adjacency.get(node);
    if (list === undefined) return [];
    if (disabledEdges === undefined || disabledEdges.size === 0) return list.slice();
    return list.filter((edge) => !disabledEdges.has(edge.id));
  }
}
