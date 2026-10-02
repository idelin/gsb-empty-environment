/**
 * AcyclicTreeCrdt — a state-based/op-log CRDT coordinating `move` and `rename`
 * on a hierarchical virtual directory tree across replicas.
 *
 * Convergence & acyclicity strategy
 * ---------------------------------
 * Every replica keeps a monotonically growing op log. Merging is the union of
 * logs (idempotent, commutative, associative). The materialized tree is a pure
 * reduction of the log in the deterministic total order (lamport, replica, seq):
 *
 *   1. `create` registers a node under its parent.
 *   2. `rename` is last-writer-wins under the total order.
 *   3. `move` is applied only if it cannot introduce a cycle: we perform
 *      *parent causal tracing* — walking the ancestor chain from the proposed
 *      new parent at that point of the reduction. If the moved node is
 *      encountered, the op is dropped (recorded in `droppedOps`).
 *
 * Because every replica reduces the same log in the same total order with the
 * same deterministic rule, all replicas converge to the same acyclic forest
 * and independently "undo" the same later operation that would close a ring.
 */

import {
  ROOT_ID,
  compareOps,
  type CreateOp,
  type DroppedOp,
  type MoveOp,
  type NodeId,
  type NodeState,
  type Op,
  type RenameOp,
  type ReplicaId,
  type Timestamp,
} from "./types.ts";

interface MutableNode {
  name: string;
  parent: NodeId | null;
}

export class AcyclicTreeCrdt {
  readonly replicaId: ReplicaId;
  private lamport = 0;
  private seq = 0;
  private readonly log: Map<string, Op> = new Map();

  constructor(replicaId: ReplicaId) {
    if (!replicaId) throw new Error("replicaId must be non-empty");
    this.replicaId = replicaId;
  }

  // ------------------------------------------------------------------ ops --

  private nextTs(): Timestamp {
    this.lamport += 1;
    this.seq += 1;
    return { lamport: this.lamport, replica: this.replicaId, seq: this.seq };
  }

  private append(op: Op): Op {
    this.log.set(op.id, op);
    return op;
  }

  /** Create a directory node under `parent` (defaults to root). */
  create(name: string, parent: NodeId = ROOT_ID): NodeId {
    const ts = this.nextTs();
    const node: NodeId = `n@${ts.replica}#${ts.seq}`;
    const op: CreateOp = { kind: "create", id: `create:${node}`, ts, node, name, parent };
    this.append(op);
    return node;
  }

  /** Move `node` under `newParent`. Cycle safety is enforced at reduction time. */
  move(node: NodeId, newParent: NodeId): MoveOp {
    const ts = this.nextTs();
    const op: MoveOp = {
      kind: "move",
      id: `move:${ts.replica}#${ts.seq}`,
      ts,
      node,
      newParent,
    };
    return this.append(op) as MoveOp;
  }

  /** Rename `node` (last-writer-wins under the deterministic total order). */
  rename(node: NodeId, name: string): RenameOp {
    const ts = this.nextTs();
    const op: RenameOp = {
      kind: "rename",
      id: `rename:${ts.replica}#${ts.seq}`,
      ts,
      node,
      name,
    };
    return this.append(op) as RenameOp;
  }

  // ---------------------------------------------------------------- merge --

  /** Merge another replica (or a raw op list) into this replica. */
  merge(other: AcyclicTreeCrdt | readonly Op[]): void {
    const ops = other instanceof AcyclicTreeCrdt ? other.ops() : other;
    for (const op of ops) {
      this.log.set(op.id, op);
      if (op.ts.lamport > this.lamport) this.lamport = op.ts.lamport;
    }
  }

  /** Snapshot of the local op log (unsorted). */
  ops(): Op[] {
    return [...this.log.values()];
  }

  // ------------------------------------------------------------- reduction --

  /**
   * Reduce the op log in deterministic total order into a parent/name map.
   * Returns the map plus the list of ops that were dropped (cycle / dangling).
   */
  private reduce(): { nodes: Map<NodeId, MutableNode>; dropped: DroppedOp[] } {
    const nodes = new Map<NodeId, MutableNode>();
    nodes.set(ROOT_ID, { name: "/", parent: null });
    const dropped: DroppedOp[] = [];

    const sorted = [...this.log.values()].sort(compareOps);
    for (const op of sorted) {
      switch (op.kind) {
        case "create": {
          if (!nodes.has(op.parent)) {
            dropped.push({ op, reason: `unknown parent ${op.parent}` });
            break;
          }
          nodes.set(op.node, { name: op.name, parent: op.parent });
          break;
        }
        case "rename": {
          const target = nodes.get(op.node);
          if (!target) {
            dropped.push({ op, reason: `unknown node ${op.node}` });
            break;
          }
          target.name = op.name;
          break;
        }
        case "move": {
          const target = nodes.get(op.node);
          if (!target) {
            dropped.push({ op, reason: `unknown node ${op.node}` });
            break;
          }
          if (!nodes.has(op.newParent)) {
            dropped.push({ op, reason: `unknown parent ${op.newParent}` });
            break;
          }
          if (this.introducesCycle(nodes, op.node, op.newParent)) {
            dropped.push({
              op,
              reason: `cycle: moving ${op.node} under ${op.newParent} would orphan a ring`,
            });
            break;
          }
          target.parent = op.newParent;
          break;
        }
      }
    }
    return { nodes, dropped };
  }

  /**
   * Parent causal tracing: walk the ancestor chain from `newParent`; if we
   * reach `node`, placing `node` there would close a topological ring.
   */
  private introducesCycle(
    nodes: Map<NodeId, MutableNode>,
    node: NodeId,
    newParent: NodeId,
  ): boolean {
    if (node === newParent) return true;
    let cursor: NodeId | null = newParent;
    const seen = new Set<NodeId>();
    while (cursor !== null) {
      if (cursor === node) return true;
      if (seen.has(cursor)) return true; // defensive: pre-existing corruption
      seen.add(cursor);
      cursor = nodes.get(cursor)?.parent ?? null;
    }
    return false;
  }

  // --------------------------------------------------------------- queries --

  /** Ops that lost arbitration during the last reduction (cycle breakers). */
  droppedOps(): DroppedOp[] {
    return this.reduce().dropped;
  }

  /** Materialized tree state. */
  state(): NodeState[] {
    const { nodes } = this.reduce();
    const children = new Map<NodeId, NodeId[]>();
    for (const id of nodes.keys()) children.set(id, []);
    for (const [id, n] of nodes) {
      if (n.parent !== null) children.get(n.parent)!.push(id);
    }
    for (const list of children.values()) list.sort();
    return [...nodes.entries()]
      .map(([id, n]) => ({
        id,
        name: n.name,
        parent: n.parent,
        children: children.get(id)!,
      }))
      .sort((a, b) => (a.id < b.id ? -1 : 1));
  }

  /** Absolute path of a node, e.g. "/dirB/dirA". Throws if unknown. */
  pathOf(node: NodeId): string {
    const { nodes } = this.reduce();
    if (!nodes.has(node)) throw new Error(`unknown node ${node}`);
    const parts: string[] = [];
    let cursor: NodeId | null = node;
    while (cursor !== null && cursor !== ROOT_ID) {
      parts.unshift(nodes.get(cursor)!.name);
      cursor = nodes.get(cursor)!.parent;
    }
    return "/" + parts.join("/");
  }

  /** True iff `ancestor` is a strict ancestor of `node` in the current state. */
  isAncestor(ancestor: NodeId, node: NodeId): boolean {
    const { nodes } = this.reduce();
    let cursor = nodes.get(node)?.parent ?? null;
    while (cursor !== null) {
      if (cursor === ancestor) return true;
      cursor = nodes.get(cursor)?.parent ?? null;
    }
    return false;
  }

  /** Every node must remain reachable from root (no orphaned ring). */
  allReachable(): boolean {
    const { nodes } = this.reduce();
    for (const id of nodes.keys()) {
      let cursor: NodeId | null = id;
      const seen = new Set<NodeId>();
      let ok = false;
      while (cursor !== null) {
        if (cursor === ROOT_ID) {
          ok = true;
          break;
        }
        if (seen.has(cursor)) break;
        seen.add(cursor);
        cursor = nodes.get(cursor)?.parent ?? null;
      }
      if (!ok) return false;
    }
    return true;
  }
}
