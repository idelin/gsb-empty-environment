/**
 * Dynamic control-flow-graph dominator tree.
 *
 * - Computes immediate dominators (idom) for every node reachable from an
 *   entry node using the classic iterative dataflow equations:
 *     Dom(entry) = {entry}
 *     Dom(n)     = {n} ∪ ⋂_{p ∈ preds(n)} Dom(p)
 * - Supports incremental, local updates after inserting/removing a single
 *   edge: only the region reachable from the edge's target (the only nodes
 *   whose dominator sets can change) is recomputed to a fixpoint.
 * - Emits counterexample witness paths for non-dominance queries: a concrete
 *   entry-to-target path that avoids the claimed dominator.
 */

export type NodeId = string;
export type Edge = readonly [NodeId, NodeId];

export class DominatorTree {
  readonly entry: NodeId;

  private readonly succ = new Map<NodeId, Set<NodeId>>();
  private readonly pred = new Map<NodeId, Set<NodeId>>();

  /** Nodes reachable from the entry. */
  private reachable = new Set<NodeId>();
  /** Dominator set per reachable node. */
  private dom = new Map<NodeId, Set<NodeId>>();
  /** Immediate dominator per reachable node (entry maps to null). */
  private idomMap = new Map<NodeId, NodeId | null>();

  constructor(entry: NodeId, edges: Iterable<Edge> = []) {
    this.entry = entry;
    this.ensureNode(entry);
    for (const [u, v] of edges) this.insertEdgeRaw(u, v);
    this.recomputeAll();
  }

  // ---------------------------------------------------------------- graph --

  private ensureNode(n: NodeId): void {
    if (!this.succ.has(n)) this.succ.set(n, new Set());
    if (!this.pred.has(n)) this.pred.set(n, new Set());
  }

  private insertEdgeRaw(u: NodeId, v: NodeId): void {
    this.ensureNode(u);
    this.ensureNode(v);
    this.succ.get(u)!.add(v);
    this.pred.get(v)!.add(u);
  }

  nodes(): NodeId[] {
    return [...this.succ.keys()];
  }

  edges(): Edge[] {
    const out: Edge[] = [];
    for (const [u, vs] of this.succ) for (const v of vs) out.push([u, v]);
    return out;
  }

  isReachable(n: NodeId): boolean {
    return this.reachable.has(n);
  }

  // ------------------------------------------------------------- queries --

  /** Immediate dominator of `n`, or null for the entry / unreachable nodes. */
  idom(n: NodeId): NodeId | null {
    return this.idomMap.get(n) ?? null;
  }

  /** Full dominator set of `n` (empty for unreachable nodes). */
  dominators(n: NodeId): Set<NodeId> {
    return new Set(this.dom.get(n) ?? []);
  }

  /** Does `a` dominate `b`? (Every entry→b path passes through `a`.) */
  dominates(a: NodeId, b: NodeId): boolean {
    const set = this.dom.get(b);
    return set !== undefined && set.has(a);
  }

  /**
   * Counterexample for the claim "`a` dominates `b`".
   *
   * Returns a concrete entry→b path that never visits `a`, proving that `a`
   * does not dominate `b`; returns null when `a` genuinely dominates `b`
   * (no such path exists).
   */
  counterexample(a: NodeId, b: NodeId): NodeId[] | null {
    if (!this.reachable.has(b)) return null;
    if (a === b) return null; // a node always dominates itself
    const parent = new Map<NodeId, NodeId | null>([[this.entry, null]]);
    const queue: NodeId[] = [this.entry];
    while (queue.length > 0) {
      const u = queue.shift()!;
      if (u === b) break;
      for (const v of this.succ.get(u) ?? []) {
        if (v === a || parent.has(v)) continue;
        parent.set(v, u);
        queue.push(v);
      }
    }
    if (!parent.has(b)) return null; // every path to b passes through a
    const path: NodeId[] = [];
    for (let cur: NodeId | null = b; cur !== null; cur = parent.get(cur)!)
      path.push(cur);
    return path.reverse();
  }

  /** Check that `path` is a real entry→target path avoiding `avoid`. */
  isValidWitness(path: NodeId[], target: NodeId, avoid: NodeId): boolean {
    if (path.length === 0 || path[0] !== this.entry) return false;
    if (path[path.length - 1] !== target) return false;
    if (path.includes(avoid)) return false;
    for (let i = 0; i + 1 < path.length; i++) {
      if (!this.succ.get(path[i])?.has(path[i + 1])) return false;
    }
    return true;
  }

  // ------------------------------------------------------------- updates --

  /** Insert edge u→v and incrementally refresh the affected region. */
  addEdge(u: NodeId, v: NodeId): void {
    this.insertEdgeRaw(u, v);
    this.incrementalUpdate(v);
  }

  /** Remove edge u→v and incrementally refresh the affected region. */
  removeEdge(u: NodeId, v: NodeId): void {
    this.succ.get(u)?.delete(v);
    this.pred.get(v)?.delete(u);
    this.incrementalUpdate(v);
  }

  /**
   * Incremental local update after an edge touching `target` changed.
   *
   * Key fact: inserting or removing an edge u→v can only change Dom(w) for
   * nodes w reachable from v (any new/removed entry→w path must pass through
   * v). We therefore recompute the dataflow fixpoint only over
   * `affected = {v} ∪ descendants(v)`, treating dominator sets of nodes
   * outside the region as fixed boundary values.
   */
  private incrementalUpdate(target: NodeId): void {
    const newReachable = this.computeReachable();

    // Region whose dominator sets may change.
    const affected = new Set<NodeId>();
    const stack: NodeId[] = [target];
    while (stack.length > 0) {
      const n = stack.pop()!;
      if (affected.has(n) || !newReachable.has(n)) continue;
      affected.add(n);
      for (const m of this.succ.get(n) ?? []) stack.push(m);
    }

    // Drop state for nodes that fell out of the reachable set.
    for (const n of [...this.dom.keys()]) {
      if (!newReachable.has(n)) {
        this.dom.delete(n);
        this.idomMap.delete(n);
      }
    }
    this.reachable = newReachable;

    // Re-initialise affected nodes to the top element (all reachable nodes)
    // so the monotone fixpoint converges to the greatest solution.
    for (const n of affected) {
      this.dom.set(n, n === this.entry ? new Set([this.entry]) : new Set(newReachable));
    }

    // Local fixpoint iteration over the affected region only.
    let changed = true;
    while (changed) {
      changed = false;
      for (const n of affected) {
        if (n === this.entry) continue;
        const next = this.meetOverPredecessors(n);
        if (!setsEqual(next, this.dom.get(n)!)) {
          this.dom.set(n, next);
          changed = true;
        }
      }
    }

    this.recomputeIdom(affected);
  }

  // --------------------------------------------------------- full build --

  private recomputeAll(): void {
    this.reachable = this.computeReachable();
    this.dom.clear();
    for (const n of this.reachable) {
      this.dom.set(n, n === this.entry ? new Set([this.entry]) : new Set(this.reachable));
    }
    let changed = true;
    while (changed) {
      changed = false;
      for (const n of this.reachable) {
        if (n === this.entry) continue;
        const next = this.meetOverPredecessors(n);
        if (!setsEqual(next, this.dom.get(n)!)) {
          this.dom.set(n, next);
          changed = true;
        }
      }
    }
    this.recomputeIdom(new Set(this.reachable));
  }

  private computeReachable(): Set<NodeId> {
    const seen = new Set<NodeId>([this.entry]);
    const queue: NodeId[] = [this.entry];
    while (queue.length > 0) {
      const u = queue.shift()!;
      for (const v of this.succ.get(u) ?? []) {
        if (!seen.has(v)) {
          seen.add(v);
          queue.push(v);
        }
      }
    }
    return seen;
  }

  /** Dom(n) = {n} ∪ ⋂ Dom(p) over reachable predecessors p of n. */
  private meetOverPredecessors(n: NodeId): Set<NodeId> {
    const preds = [...(this.pred.get(n) ?? [])].filter((p) => this.reachable.has(p));
    const result = new Set<NodeId>([n]);
    if (preds.length === 0) return result;
    let acc = new Set(this.dom.get(preds[0]) ?? [preds[0]]);
    for (let i = 1; i < preds.length; i++) {
      const other = this.dom.get(preds[i]) ?? new Set([preds[i]]);
      for (const x of [...acc]) if (!other.has(x)) acc.delete(x);
    }
    for (const x of acc) result.add(x);
    return result;
  }

  /**
   * Derive idom from dominator sets: idom(n) is the strict dominator of n
   * with the largest dominator set (it is dominated by every other strict
   * dominator of n).
   */
  private recomputeIdom(region: Set<NodeId>): void {
    for (const n of region) {
      if (n === this.entry || !this.reachable.has(n)) {
        this.idomMap.set(n, null);
        continue;
      }
      const strict = [...this.dom.get(n)!].filter((d) => d !== n);
      let best: NodeId | null = null;
      let bestSize = -1;
      for (const d of strict) {
        const size = this.dom.get(d)?.size ?? 0;
        if (size > bestSize) {
          best = d;
          bestSize = size;
        }
      }
      this.idomMap.set(n, best);
    }
  }
}

function setsEqual(a: Set<NodeId>, b: Set<NodeId>): boolean {
  if (a.size !== b.size) return false;
  for (const x of a) if (!b.has(x)) return false;
  return true;
}
