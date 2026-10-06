export type NodeId = string;

export type RefStatus = "ok" | "broken";

/** A typed, directed reference from one node to another. */
export interface Ref {
  /** Slot name on the owning node, unique per node. */
  key: string;
  /** The node type the target must have for the reference to be valid. */
  expectedType: string;
  /** Resolved target id, or null when the reference is broken. */
  target: NodeId | null;
  status: RefStatus;
  /** Original target id, kept for diagnostics when the reference is broken. */
  brokenTarget?: NodeId;
}

export interface CanvasNode {
  id: NodeId;
  type: string;
  props: Record<string, unknown>;
  refs: Ref[];
}

export function cloneNode(node: CanvasNode): CanvasNode {
  return structuredClone(node);
}

export class CanvasError extends Error {}

/** The canvas: an owned set of nodes with a capacity invariant. */
export class Canvas {
  private readonly nodes = new Map<NodeId, CanvasNode>();
  readonly capacity: number;

  constructor(capacity: number = Number.POSITIVE_INFINITY) {
    this.capacity = capacity;
  }

  get size(): number {
    return this.nodes.size;
  }

  has(id: NodeId): boolean {
    return this.nodes.has(id);
  }

  get(id: NodeId): CanvasNode | undefined {
    return this.nodes.get(id);
  }

  ids(): NodeId[] {
    return [...this.nodes.keys()];
  }

  addNode(node: CanvasNode): void {
    if (this.nodes.has(node.id)) {
      throw new CanvasError(`duplicate node id: ${node.id}`);
    }
    if (this.nodes.size + 1 > this.capacity) {
      throw new CanvasError(`canvas capacity ${this.capacity} exceeded`);
    }
    this.nodes.set(node.id, node);
  }

  removeNode(id: NodeId): void {
    if (!this.nodes.delete(id)) {
      throw new CanvasError(`unknown node id: ${id}`);
    }
  }
}
