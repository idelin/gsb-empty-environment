import {
  compareOps,
  opKey,
  type BaseNode,
  type MoveOp,
  type TreeNode,
} from "./types.ts";

export interface AppliedOp {
  readonly op: MoveOp;
  /** False when the op was undone because it would close a dependency cycle. */
  readonly applied: boolean;
  /** Human-readable arbitration reason when the op was undone. */
  readonly reason?: string;
}

/**
 * Acyclic CRDT tree: coordinates concurrent rename/move operations on a
 * replicated hierarchical directory tree so that the materialised state on
 * every replica converges to the same cycle-free tree.
 *
 * Algorithm (Kleppmann-style highly-available move):
 *   1. Every replica keeps the full operation log.
 *   2. The tree is materialised by replaying the log in the deterministic
 *      total order (Lamport time, then replica id) on top of the base tree.
 *   3. Before applying an op, the parent chain of the destination is traced
 *      (causal ancestry walk). If the moving node is an ancestor of the
 *      destination, applying it would close a topological dependency cycle,
 *      so the op is UNDONE (skipped). Because earlier ops in the total order
 *      are already in place, the op that gets undone is always the LATER one
 *      of the conflicting pair — deterministic arbitration, no data loss:
 *      both directories remain reachable from the root.
 */
export class CrdtTree {
  readonly #base: ReadonlyMap<string, BaseNode>;
  readonly #log = new Map<string, MoveOp>();
  #state = new Map<string, TreeNode>();
  #verdicts = new Map<string, AppliedOp>();

  constructor(baseNodes: readonly BaseNode[]) {
    this.#base = new Map(baseNodes.map((n) => [n.id, n]));
    this.#recompute();
  }

  /** Merge a (possibly remote) operation into the log. Idempotent. */
  apply(op: MoveOp): void {
    const key = opKey(op);
    if (this.#log.has(key)) return;
    if (!this.#base.has(op.node)) {
      throw new Error(`move op references unknown node "${op.node}"`);
    }
    if (!this.#base.has(op.parent)) {
      throw new Error(`move op references unknown parent "${op.parent}"`);
    }
    this.#log.set(key, op);
    this.#recompute();
  }

  /** Merge a batch of operations (e.g. a remote replica's log). */
  merge(ops: Iterable<MoveOp>): void {
    let changed = false;
    for (const op of ops) {
      const key = opKey(op);
      if (this.#log.has(key)) continue;
      if (!this.#base.has(op.node) || !this.#base.has(op.parent)) {
        throw new Error("move op references unknown node");
      }
      this.#log.set(key, op);
      changed = true;
    }
    if (changed) this.#recompute();
  }

  /** The full operation log, in deterministic total order. */
  get log(): readonly MoveOp[] {
    return [...this.#log.values()].sort(compareOps);
  }

  /** Materialised tree state, keyed by node id. */
  get state(): ReadonlyMap<string, TreeNode> {
    return this.#state;
  }

  /** Arbitration verdict for every op in the log (applied or undone). */
  get verdicts(): readonly AppliedOp[] {
    return this.log.map((op) => this.#verdicts.get(opKey(op))!);
  }

  /** Absolute path of a node, e.g. "/dirB/dirA". */
  pathOf(id: string): string {
    const parts: string[] = [];
    const seen = new Set<string>();
    let cur: TreeNode | undefined = this.#state.get(id);
    if (!cur) throw new Error(`unknown node "${id}"`);
    while (cur && cur.parent !== null) {
      if (seen.has(cur.id)) throw new Error("corrupt state: cycle detected");
      seen.add(cur.id);
      parts.unshift(cur.name);
      cur = this.#state.get(cur.parent);
    }
    return "/" + parts.join("/");
  }

  /** Ids of all nodes reachable from the root (orphan / data-loss check). */
  reachableFrom(rootId: string): Set<string> {
    const children = new Map<string | null, string[]>();
    for (const node of this.#state.values()) {
      const list = children.get(node.parent) ?? [];
      list.push(node.id);
      children.set(node.parent, list);
    }
    const out = new Set<string>();
    const queue = [rootId];
    while (queue.length > 0) {
      const id = queue.pop()!;
      if (out.has(id)) continue;
      out.add(id);
      for (const child of children.get(id) ?? []) queue.push(child);
    }
    return out;
  }

  /** Replay the log in total order, undoing cycle-closing ops. */
  #recompute(): void {
    const state = new Map<string, { name: string; parent: string | null }>();
    for (const [id, base] of this.#base) {
      state.set(id, { name: base.name, parent: base.parent });
    }
    this.#verdicts = new Map();

    for (const op of this.log) {
      const verdict = this.#tryApply(state, op);
      this.#verdicts.set(opKey(op), verdict);
    }

    this.#resolveNameConflicts(state);
    this.#state = new Map(
      [...state].map(([id, n]) => [id, { id, name: n.name, parent: n.parent }]),
    );
  }

  #tryApply(
    state: Map<string, { name: string; parent: string | null }>,
    op: MoveOp,
  ): AppliedOp {
    if (op.node === op.parent) {
      return { op, applied: false, reason: "cannot move a node into itself" };
    }
    // Causal ancestry trace: walk the destination's parent chain. If the
    // moving node is an ancestor of its destination, this op closes a cycle.
    const seen = new Set<string>();
    let cursor: string | null = op.parent;
    while (cursor !== null) {
      if (cursor === op.node) {
        return {
          op,
          applied: false,
          reason:
            `undone: moving "${op.node}" under "${op.parent}" would close a ` +
            `dependency cycle (later op in total order loses)`,
        };
      }
      if (seen.has(cursor)) break; // defensive: pre-existing corruption
      seen.add(cursor);
      cursor = state.get(cursor)?.parent ?? null;
    }
    const target = state.get(op.node)!;
    target.parent = op.parent;
    target.name = op.name;
    return { op, applied: true };
  }

  /**
   * Deterministic sibling name-conflict resolution: when two children of the
   * same parent end up with the same name, the loser keeps its name suffixed
   * with `#<nodeId>` so no directory is silently overwritten.
   */
  #resolveNameConflicts(
    state: Map<string, { name: string; parent: string | null }>,
  ): void {
    const byName = new Map<string, string[]>();
    for (const [id, n] of state) {
      if (n.parent === null) continue;
      const key = `${n.parent}${n.name}`;
      const list = byName.get(key) ?? [];
      list.push(id);
      byName.set(key, list);
    }
    for (const ids of byName.values()) {
      if (ids.length < 2) continue;
      ids.sort();
      for (const id of ids.slice(1)) {
        state.get(id)!.name = `${state.get(id)!.name}#${id}`;
      }
    }
  }
}
