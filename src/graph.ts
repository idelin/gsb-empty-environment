export interface NodeInput {
  id: string;
  parentId?: string | null;
}

export interface EdgeInput {
  id: string;
  source: string;
  target: string;
}

export interface GraphNode {
  id: string;
  parentId: string | null;
}

export interface GraphEdge {
  id: string;
  source: string;
  target: string;
}

/**
 * Original graph: a node forest (each node has at most one parent) plus
 * edges with stable ids. The graph is never mutated by collapse/expand;
 * views are always derived from these original edges.
 */
export class Graph {
  private readonly nodes = new Map<string, GraphNode>();
  private readonly edges = new Map<string, GraphEdge>();
  private readonly childrenIndex = new Map<string, string[]>();

  addNode(input: NodeInput): GraphNode {
    if (this.nodes.has(input.id)) {
      throw new Error(`duplicate node id: ${input.id}`);
    }
    const parentId = input.parentId ?? null;
    if (parentId !== null && !this.nodes.has(parentId)) {
      throw new Error(`unknown parent node: ${parentId}`);
    }
    const node: GraphNode = { id: input.id, parentId };
    this.nodes.set(node.id, node);
    if (parentId !== null) {
      const siblings = this.childrenIndex.get(parentId);
      if (siblings) {
        siblings.push(node.id);
      } else {
        this.childrenIndex.set(parentId, [node.id]);
      }
    }
    return node;
  }

  addEdge(input: EdgeInput): GraphEdge {
    if (this.edges.has(input.id)) {
      throw new Error(`duplicate edge id: ${input.id}`);
    }
    if (!this.nodes.has(input.source)) {
      throw new Error(`unknown edge source: ${input.source}`);
    }
    if (!this.nodes.has(input.target)) {
      throw new Error(`unknown edge target: ${input.target}`);
    }
    const edge: GraphEdge = { id: input.id, source: input.source, target: input.target };
    this.edges.set(edge.id, edge);
    return edge;
  }

  hasNode(id: string): boolean {
    return this.nodes.has(id);
  }

  getNode(id: string): GraphNode {
    const node = this.nodes.get(id);
    if (!node) {
      throw new Error(`unknown node: ${id}`);
    }
    return node;
  }

  getEdge(id: string): GraphEdge {
    const edge = this.edges.get(id);
    if (!edge) {
      throw new Error(`unknown edge: ${id}`);
    }
    return edge;
  }

  /** Node ids in insertion order. */
  nodeIds(): string[] {
    return [...this.nodes.keys()];
  }

  /** Edge ids in insertion order. */
  edgeIds(): string[] {
    return [...this.edges.keys()];
  }

  parentOf(id: string): string | null {
    return this.getNode(id).parentId;
  }

  childrenOf(id: string): string[] {
    return this.childrenIndex.get(id) ?? [];
  }
}
