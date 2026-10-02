export type NodeId = string;

/**
 * Dynamic dominator tree over a control-flow graph.
 *
 * Maintains, for every node reachable from a fixed entry node, its set of
 * dominators and its immediate dominator (idom). Supports incremental local
 * updates after single-edge insertion/deletion, and produces counterexample
 * paths (entry -> target avoiding a given node) for non-dominance queries.
 *
 * Incremental strategy (warm-started dataflow, localized to the affected
 * region):
 *  - Edge insertion u->v can only *shrink* dominator sets, so the current
 *    sets are a valid seed; iteration runs to a fixpoint.
 *  - Edge deletion u->v can only *grow* dominator sets, and only for nodes
 *    dominated by v (the subtree of v in the current dominator tree). Those
 *    seeds are reset to "all reachable nodes" before iterating to a fixpoint.
 */
export class DominatorTree {
  readonly entry: NodeId;

  private readonly succ = new Map<NodeId, Set<NodeId>>();
  private readonly pred = new Map<NodeId, Set<NodeId>>();

  /** Dominator sets for reachable nodes only. */
  private dom = new Map<NodeId, Set<NodeId>>();
  private idomMap = new Map<NodeId, NodeId>();
  /** Children in the dominator tree (for locating the affected subtree). */
  private treeChildren = new Map<NodeId, Set<NodeId>>();

  constructor(entry: NodeId, edges: ReadonlyArray<readonly [NodeId, NodeId]> = []) {
    this.entry = entry;
    this.ensureNode(entry);
    for (const [u, v] of edges) {
      this.ensureNode(u);
      this.ensureNode(v);
      this.succ.get(u)!.add(v);
      this.pred.get(v)!.add(u);
    }
    this.recomputeFull();
  }

  // ---------------------------------------------------------------- queries

  /** Immediate dominator of `n`, or null if n is the entry or unreachable. */
  idom(n: NodeId): NodeId | null {
    return this.idomMap.get(n) ?? null;
  }

  /** All dominators of `n` (including `n` itself); empty if unreachable. */
  dominatorsOf(n: NodeId): ReadonlySet<NodeId> {
    return this.dom.get(n) ?? new Set<NodeId>();
  }

  /** True iff every path from the entry to `n` passes through `d`. */
  dominates(d: NodeId, n: NodeId): boolean {
    const set = this.dom.get(n);
    return set !== undefined && set.has(d);
  }

  /** Nodes currently reachable from the entry. */
  reachable(): ReadonlySet<NodeId> {
    return new Set(this.dom.keys());
  }

  /** Children of `n` in the dominator tree. */
  childrenOf(n: NodeId): ReadonlySet<NodeId> {
    return this.treeChildren.get(n) ?? new Set<NodeId>();
  }

  /**
   * Counterexample generation: if `d` does NOT dominate `target`, return a
   * concrete path entry -> target that avoids `d` (a proof witness).
   * Returns null when `d` dominates `target` (no such path exists).
   */
  proveNotDominates(d: NodeId, target: NodeId): NodeId[] | null {
    if (!this.dom.has(target)) return null; // unreachable: vacuously dominated
    if (this.dominates(d, target)) return null;
    return this.findPathAvoiding(d, target);
  }

  // -------------------------------------------------------------- mutation

  addEdge(u: NodeId, v: NodeId): void {
    this.ensureNode(u);
    this.ensureNode(v);
    const outs = this.succ.get(u)!;
    if (outs.has(v)) return; // no-op: edge already present
    outs.add(v);
    this.pred.get(v)!.add(u);
    this.incrementalInsert();
  }

  removeEdge(u: NodeId, v: NodeId): void {
    const outs = this.succ.get(u);
    if (!outs || !outs.has(v)) return; // no-op: edge absent
    outs.delete(v);
    this.pred.get(v)!.delete(u);
    this.incrementalDelete(v);
  }

  // ----------------------------------------------------------- internals

  private ensureNode(n: NodeId): void {
    if (!this.succ.has(n)) this.succ.set(n, new Set());
    if (!this.pred.has(n)) this.pred.set(n, new Set());
  }

  private reachableNodes(): Set<NodeId> {
    const seen = new Set<NodeId>([this.entry]);
    const stack: NodeId[] = [this.entry];
    while (stack.length > 0) {
      const n = stack.pop()!;
      for (const m of this.succ.get(n)!) {
        if (!seen.has(m)) {
          seen.add(m);
          stack.push(m);
        }
      }
    }
    return seen;
  }

  /** Full recompute from scratch (used by the constructor). */
  private recomputeFull(): void {
    const reachable = this.reachableNodes();
    this.dom = new Map();
    for (const n of reachable) {
      this.dom.set(n, n === this.entry ? new Set([this.entry]) : new Set(reachable));
    }
    this.iterateToFixpoint(reachable);
  }

  /**
   * Insertion only removes paths never... rather: it only adds paths, so
   * dominator sets can only shrink. The current sets are already a superset
   * of the new fixpoint, hence a valid warm seed. Newly reachable nodes are
   * seeded with the full reachable set.
   */
  private incrementalInsert(): void {
    const reachable = this.reachableNodes();
    for (const n of reachable) {
      const set = this.dom.get(n);
      if (set === undefined) {
        this.dom.set(n, n === this.entry ? new Set([this.entry]) : new Set(reachable));
      } else {
        for (const x of set) if (!reachable.has(x)) set.delete(x);
      }
    }
    for (const n of [...this.dom.keys()]) {
      if (!reachable.has(n)) this.dom.delete(n);
    }
    this.iterateToFixpoint(reachable);
  }

  /**
   * Deletion only removes paths, so dominator sets can only grow, and only
   * within the subtree of the edge target v in the current dominator tree
   * (standard result for incremental dominator maintenance). That affected
   * region is reset to the top element before the fixpoint iteration.
   */
  private incrementalDelete(v: NodeId): void {
    const reachable = this.reachableNodes();
    const affected = this.subtreeOf(v);
    for (const n of affected) {
      if (reachable.has(n) && n !== this.entry) {
        this.dom.set(n, new Set(reachable));
      }
    }
    for (const n of [...this.dom.keys()]) {
      if (!reachable.has(n)) this.dom.delete(n);
    }
    this.iterateToFixpoint(reachable);
  }

  /** Nodes dominated by `root` (including root) per the current idom tree. */
  private subtreeOf(root: NodeId): Set<NodeId> {
    const out = new Set<NodeId>();
    const stack: NodeId[] = [root];
    while (stack.length > 0) {
      const n = stack.pop()!;
      if (out.has(n)) continue;
      out.add(n);
      for (const c of this.treeChildren.get(n) ?? []) stack.push(c);
    }
    return out;
  }

  /** Classic iterative dataflow: dom(n) = {n} ∪ ⋂ dom(p) over preds p. */
  private iterateToFixpoint(reachable: ReadonlySet<NodeId>): void {
    let changed = true;
    while (changed) {
      changed = false;
      for (const n of reachable) {
        if (n === this.entry) continue;
        let next: Set<NodeId> | null = null;
        for (const p of this.pred.get(n)!) {
          const dp = this.dom.get(p);
          if (dp === undefined) continue; // unreachable predecessor
          next = next === null ? new Set(dp) : intersect(next, dp);
        }
        if (next === null) next = new Set();
        next.add(n);
        if (!setsEqual(next, this.dom.get(n)!)) {
          this.dom.set(n, next);
          changed = true;
        }
      }
    }
    this.deriveIdom(reachable);
  }

  /**
   * idom(n) is the strict dominator of n with the largest dominator set
   * (dominators of n are totally ordered by set inclusion).
   */
  private deriveIdom(reachable: ReadonlySet<NodeId>): void {
    this.idomMap = new Map();
    this.treeChildren = new Map();
    for (const n of reachable) {
      if (n === this.entry) continue;
      const dn = this.dom.get(n)!;
      let best: NodeId | null = null;
      let bestSize = -1;
      for (const d of dn) {
        if (d === n) continue;
        const size = this.dom.get(d)!.size;
        if (size > bestSize) {
          best = d;
          bestSize = size;
        }
      }
      if (best !== null) {
        this.idomMap.set(n, best);
        let kids = this.treeChildren.get(best);
        if (!kids) this.treeChildren.set(best, (kids = new Set()));
        kids.add(n);
      }
    }
  }

  /** BFS path entry -> target that never visits `avoid`. */
  private findPathAvoiding(avoid: NodeId, target: NodeId): NodeId[] | null {
    if (this.entry === avoid || target === avoid) return null;
    const prev = new Map<NodeId, NodeId | null>([[this.entry, null]]);
    const queue: NodeId[] = [this.entry];
    while (queue.length > 0) {
      const n = queue.shift()!;
      if (n === target) {
        const path: NodeId[] = [];
        for (let cur: NodeId | null = target; cur !== null; cur = prev.get(cur)!) {
          path.push(cur);
        }
        return path.reverse();
      }
      for (const m of this.succ.get(n)!) {
        if (m !== avoid && !prev.has(m)) {
          prev.set(m, n);
          queue.push(m);
        }
      }
    }
    return null;
  }
}

function intersect(a: Set<NodeId>, b: ReadonlySet<NodeId>): Set<NodeId> {
  for (const x of a) if (!b.has(x)) a.delete(x);
  return a;
}

function setsEqual(a: ReadonlySet<NodeId>, b: ReadonlySet<NodeId>): boolean {
  if (a.size !== b.size) return false;
  for (const x of a) if (!b.has(x)) return false;
  return true;
}
