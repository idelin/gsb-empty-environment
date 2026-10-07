/**
 * Canvas graph model.
 *
 * A node carries typed reference slots. Each slot either points at a live
 * node in the graph or is marked dangling (broken link).
 */

export type NodeId = string;

export interface LiveRef {
  readonly kind: 'ref';
  /** Slot name on the source node, e.g. "input", "parent". */
  readonly key: string;
  readonly targetId: NodeId;
  /** Declared type the target must have for the ref to be valid. */
  readonly targetType: string;
}

export interface DanglingRef {
  readonly kind: 'dangling';
  readonly key: string;
  /** Id that was referenced when the link broke; kept for diagnostics. */
  readonly originalTargetId: NodeId;
  readonly targetType: string;
  readonly reason: 'missing-target' | 'type-mismatch';
}

export type NodeRef = LiveRef | DanglingRef;

export interface CanvasNode {
  readonly id: NodeId;
  readonly type: string;
  readonly refs: readonly NodeRef[];
}

export class Graph {
  private readonly nodes = new Map<NodeId, CanvasNode>();

  has(id: NodeId): boolean {
    return this.nodes.has(id);
  }

  get(id: NodeId): CanvasNode | undefined {
    return this.nodes.get(id);
  }

  get size(): number {
    return this.nodes.size;
  }

  values(): IterableIterator<CanvasNode> {
    return this.nodes.values();
  }

  /** Insert a node. Throws if the id already exists. */
  add(node: CanvasNode): void {
    if (this.nodes.has(node.id)) {
      throw new Error(`node id already exists: ${node.id}`);
    }
    this.nodes.set(node.id, node);
  }

  /**
   * Atomically insert a batch of nodes: either all are inserted or none.
   * Used by paste so a failed paste never leaves a half-applied graph.
   */
  addAll(batch: readonly CanvasNode[]): void {
    for (const node of batch) {
      if (this.nodes.has(node.id)) {
        throw new Error(`node id already exists: ${node.id}`);
      }
    }
    const seen = new Set<NodeId>();
    for (const node of batch) {
      if (seen.has(node.id)) {
        throw new Error(`duplicate node id in batch: ${node.id}`);
      }
      seen.add(node.id);
    }
    for (const node of batch) {
      this.nodes.set(node.id, node);
    }
  }

  remove(id: NodeId): void {
    if (!this.nodes.delete(id)) {
      throw new Error(`node id does not exist: ${id}`);
    }
  }

  /** Structural snapshot used by tests and undo/redo assertions. */
  snapshot(): ReadonlyMap<NodeId, CanvasNode> {
    return new Map(this.nodes);
  }
}
