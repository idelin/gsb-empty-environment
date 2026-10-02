import { CrdtTree } from "./crdt-tree.ts";
import type { BaseNode, MoveOp, TreeNode } from "./types.ts";

/**
 * A distributed replica ("node") of the directory tree. Maintains a Lamport
 * clock, issues local rename/move operations, and merges remote operations.
 */
export class Replica {
  readonly id: string;
  readonly tree: CrdtTree;
  #clock = 0;

  constructor(id: string, baseNodes: readonly BaseNode[]) {
    this.id = id;
    this.tree = new CrdtTree(baseNodes);
  }

  /** Lamport clock value (observed max so far). */
  get clock(): number {
    return this.#clock;
  }

  /** Issue a local move (and optional rename). Returns the logged op. */
  move(nodeId: string, newParentId: string, newName?: string): MoveOp {
    const current = this.tree.state.get(nodeId);
    if (!current) throw new Error(`unknown node "${nodeId}"`);
    const op: MoveOp = {
      time: ++this.#clock,
      replica: this.id,
      node: nodeId,
      parent: newParentId,
      name: newName ?? current.name,
    };
    this.tree.apply(op);
    return op;
  }

  /** Issue a local rename (move to same parent with a new name). */
  rename(nodeId: string, newName: string): MoveOp {
    const current = this.tree.state.get(nodeId);
    if (!current) throw new Error(`unknown node "${nodeId}"`);
    if (current.parent === null) throw new Error("cannot rename the root");
    return this.move(nodeId, current.parent, newName);
  }

  /** Receive an operation from a remote replica. */
  receive(op: MoveOp): void {
    this.#clock = Math.max(this.#clock, op.time) + 1;
    this.tree.apply(op);
  }

  /** Full bidirectional anti-entropy sync with another replica. */
  syncWith(other: Replica): void {
    for (const op of this.tree.log) other.receive(op);
    for (const op of other.tree.log) this.receive(op);
  }

  get state(): ReadonlyMap<string, TreeNode> {
    return this.tree.state;
  }

  pathOf(nodeId: string): string {
    return this.tree.pathOf(nodeId);
  }
}
