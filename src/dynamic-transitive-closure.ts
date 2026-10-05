/**
 * Dynamic transitive closure for a directed multigraph.
 *
 * Supports batch insertion and removal of directed edges. Edge removals only
 * drop reachability pairs that lose *every* supporting path; pairs still
 * backed by other paths are preserved. Each batch returns the reachability
 * delta (added / removed pairs) and any reachable pair can be explained with
 * a concrete witness path.
 *
 * Semantics:
 *  - The reported reachability relation contains a pair (u, v) iff there is a
 *    directed path of length >= 1 from u to v. A node reaches itself only
 *    through a non-trivial cycle (a self loop counts).
 *  - The graph is a multigraph: inserting the same edge k times requires k
 *    removals before the edge disappears.
 *  - Nodes are compared with SameValueZero (Map/Set semantics) and must not
 *    be `undefined`.
 */

export type NodePair<T> = [T, T];

export interface ReachDelta<T> {
  added: NodePair<T>[];
  removed: NodePair<T>[];
}

export interface BatchUpdate<T> {
  add?: Iterable<NodePair<T>>;
  remove?: Iterable<NodePair<T>>;
}

interface BfsRow<T> {
  reach: Set<T>;
  via: Map<T, T>;
}

function sameValueZero(a: unknown, b: unknown): boolean {
  return a === b || (a !== a && b !== b);
}

export class DynamicTransitiveClosure<T> {
  // Adjacency with multiplicity: _out.get(a).get(b) = number of parallel a->b edges.
  private _out: Map<T, Map<T, number>> = new Map();
  // Reflexive transitive closure: _reach.get(u) contains every v reachable
  // from u by a path of length >= 0 (u itself is always present).
  private _reach: Map<T, Set<T>> = new Map();
  // Reverse index of _reach: _preds.get(v) = all u with v in _reach.get(u).
  private _preds: Map<T, Set<T>> = new Map();
  // Witness bookkeeping: _via.get(u).get(v) is the predecessor of v on a
  // concrete path from u to v. An entry (u, u) exists iff u lies on a cycle.
  private _via: Map<T, Map<T, T>> = new Map();
  private _edgeCount = 0;
  private _netAdd: Map<T, Set<T>> = new Map();
  private _netDel: Map<T, Set<T>> = new Map();

  get nodeCount(): number {
    return this._reach.size;
  }

  // Number of edges counting duplicates (multigraph size).
  get edgeCount(): number {
    return this._edgeCount;
  }

  // Number of reported reachability pairs (paths of length >= 1).
  get pairCount(): number {
    let total = 0;
    for (const u of this._reach.keys()) {
      total += this.reachableFrom(u).length;
    }
    return total;
  }

  nodes(): T[] {
    return Array.from(this._reach.keys());
  }

  // Distinct edges (duplicates collapsed).
  edges(): NodePair<T>[] {
    const out: NodePair<T>[] = [];
    for (const [a, targets] of this._out) {
      for (const b of targets.keys()) {
        out.push([a, b]);
      }
    }
    return out;
  }

  hasEdge(a: T, b: T): boolean {
    const targets = this._out.get(a);
    return targets !== undefined && targets.has(b);
  }

  edgeMultiplicity(a: T, b: T): number {
    const targets = this._out.get(a);
    if (targets === undefined) return 0;
    const n = targets.get(b);
    return n === undefined ? 0 : n;
  }

  isReachable(u: T, v: T): boolean {
    const row = this._reach.get(u);
    if (row === undefined || !row.has(v)) return false;
    if (sameValueZero(u, v)) {
      const via = this._via.get(u);
      return via !== undefined && via.has(u);
    }
    return true;
  }

  // All v with a path of length >= 1 from u to v (includes u if u is on a cycle).
  reachableFrom(u: T): T[] {
    const row = this._reach.get(u);
    if (row === undefined) return [];
    const out: T[] = [];
    for (const v of row) {
      if (!sameValueZero(u, v)) out.push(v);
    }
    const via = this._via.get(u);
    if (via !== undefined && via.has(u)) out.push(u);
    return out;
  }

  // All reported reachability pairs.
  snapshot(): NodePair<T>[] {
    const out: NodePair<T>[] = [];
    for (const u of this._reach.keys()) {
      for (const v of this.reachableFrom(u)) {
        out.push([u, v]);
      }
    }
    return out;
  }

  // A concrete directed path from u to v in the current graph, or null.
  witnessPath(u: T, v: T): T[] | null {
    if (!this.isReachable(u, v)) return null;
    const via = this._via.get(u);
    if (via === undefined) return null;
    if (sameValueZero(u, v)) {
      const cycle: T[] = [u];
      let cur: T = this._viaGet(u, u);
      while (!sameValueZero(cur, u)) {
        cycle.push(cur);
        cur = this._viaGet(u, cur);
      }
      cycle.push(u);
      return cycle.reverse();
    }
    const path: T[] = [v];
    let cur: T = v;
    while (!sameValueZero(cur, u)) {
      cur = this._viaGet(u, cur);
      path.push(cur);
    }
    return path.reverse();
  }

  addEdge(a: T, b: T): ReachDelta<T> {
    return this.applyBatch({ add: [[a, b]] });
  }

  removeEdge(a: T, b: T): ReachDelta<T> {
    return this.applyBatch({ remove: [[a, b]] });
  }

  clear(): void {
    this._out.clear();
    this._reach.clear();
    this._preds.clear();
    this._via.clear();
    this._edgeCount = 0;
    this._netAdd.clear();
    this._netDel.clear();
  }

  /**
   * Applies a batch of edge insertions and removals atomically with respect
   * to validation: if any removal targets an edge that is not present
   * (counting duplicates within the batch), an error is thrown and the graph
   * is left untouched.
   *
   * Returns the net reachability delta: pairs reported in `added` were not
   * reachable before the batch and are reachable after; pairs in `removed`
   * were reachable before and are not reachable after. Pairs that lose one
   * supporting path but keep another never appear in the delta.
   */
  applyBatch(update: BatchUpdate<T>): ReachDelta<T> {
    const adds: NodePair<T>[] = update.add === undefined ? [] : Array.from(update.add);
    const rems: NodePair<T>[] = update.remove === undefined ? [] : Array.from(update.remove);

    // Validate removals before mutating anything.
    const need: Map<T, Map<T, number>> = new Map();
    for (const [a, b] of rems) {
      let t = need.get(a);
      if (t === undefined) {
        t = new Map();
        need.set(a, t);
      }
      const cur = t.get(b);
      t.set(b, (cur === undefined ? 0 : cur) + 1);
    }
    for (const [a, t] of need) {
      for (const [b, n] of t) {
        if (this.edgeMultiplicity(a, b) < n) {
          throw new Error(
            'cannot remove edge that is not present: ' + String(a) + ' -> ' + String(b)
          );
        }
      }
    }

    this._netAdd = new Map();
    this._netDel = new Map();

    // Phase 1: removals. Reachability can only shrink here; only sources
    // that could reach the tail of a deleted edge may lose pairs, so exactly
    // those rows are recomputed with a BFS on the updated graph.
    const zeroed: NodePair<T>[] = [];
    for (const [a, b] of rems) {
      const targets = this._out.get(a);
      if (targets === undefined) continue;
      const n = targets.get(b);
      if (n === undefined) continue;
      if (n <= 1) {
        targets.delete(b);
        zeroed.push([a, b]);
      } else {
        targets.set(b, n - 1);
      }
    }
    if (zeroed.length > 0) {
      const affected: Set<T> = new Set();
      for (const [a] of zeroed) {
        const preds = this._preds.get(a);
        if (preds === undefined) continue;
        for (const u of preds) affected.add(u);
      }
      for (const u of affected) this._recomputeSource(u);
    }

    // Phase 2: insertions. Reachability can only grow; each genuinely new
    // edge (a, b) contributes the product (sources of a) x (targets of b).
    for (const [a, b] of adds) {
      this._ensureNode(a);
      this._ensureNode(b);
      let targets = this._out.get(a);
      if (targets === undefined) {
        targets = new Map();
        this._out.set(a, targets);
      }
      const cur = targets.get(b);
      const n = cur === undefined ? 0 : cur;
      targets.set(b, n + 1);
      if (n === 0) this._propagateInsert(a, b);
    }

    this._edgeCount += adds.length - rems.length;

    const added: NodePair<T>[] = [];
    for (const [u, vs] of this._netAdd) {
      for (const v of vs) added.push([u, v]);
    }
    const removed: NodePair<T>[] = [];
    for (const [u, vs] of this._netDel) {
      for (const v of vs) removed.push([u, v]);
    }
    return { added, removed };
  }

  private _ensureNode(x: T): void {
    if (!this._reach.has(x)) {
      this._reach.set(x, new Set([x]));
      this._preds.set(x, new Set([x]));
      this._via.set(x, new Map());
    }
  }

  private _viaGet(u: T, key: T): T {
    const row = this._via.get(u);
    if (row === undefined) throw new Error('internal error: missing witness row');
    const val = row.get(key);
    if (val === undefined) throw new Error('internal error: missing witness entry');
    return val;
  }

  // Incremental insertion of a single genuinely new edge a -> b.
  private _propagateInsert(a: T, b: T): void {
    const sources = this._preds.get(a);
    const targets = this._reach.get(b);
    if (sources === undefined || targets === undefined) return;
    const srcList = Array.from(sources);
    const dstList = Array.from(targets);
    for (const u of srcList) {
      const reachU = this._reach.get(u);
      const viaU = this._via.get(u);
      if (reachU === undefined || viaU === undefined) continue;
      for (const v of dstList) {
        if (sameValueZero(u, v)) {
          // A new non-trivial cycle through u.
          if (!viaU.has(u)) {
            let w: T;
            if (sameValueZero(b, u)) {
              w = a;
            } else {
              w = this._viaGet(b, u);
            }
            viaU.set(u, w);
            this._markAdded(u, u);
          }
        } else if (!reachU.has(v)) {
          reachU.add(v);
          const predsV = this._preds.get(v);
          if (predsV !== undefined) predsV.add(u);
          let w: T;
          if (sameValueZero(b, v)) {
            w = a;
          } else {
            w = this._viaGet(b, v);
          }
          viaU.set(v, w);
          this._markAdded(u, v);
        }
      }
    }
  }

  // Recomputes the full reachability row of one source after deletions.
  private _recomputeSource(u: T): void {
    const row = this._bfs(u);
    const oldReach = this._reach.get(u);
    const oldVia = this._via.get(u);
    const oldSelf = oldVia !== undefined && oldVia.has(u);
    const newSelf = row.via.has(u);
    if (oldReach !== undefined) {
      for (const v of oldReach) {
        if (sameValueZero(u, v)) continue;
        if (!row.reach.has(v)) {
          const predsV = this._preds.get(v);
          if (predsV !== undefined) predsV.delete(u);
          this._markRemoved(u, v);
        }
      }
    }
    for (const v of row.reach) {
      if (sameValueZero(u, v)) continue;
      if (oldReach === undefined || !oldReach.has(v)) {
        const predsV = this._preds.get(v);
        if (predsV !== undefined) predsV.add(u);
        this._markAdded(u, v);
      }
    }
    if (oldSelf && !newSelf) this._markRemoved(u, u);
    if (!oldSelf && newSelf) this._markAdded(u, u);
    this._reach.set(u, row.reach);
    this._via.set(u, row.via);
  }

  private _bfs(source: T): BfsRow<T> {
    const reach: Set<T> = new Set([source]);
    const via: Map<T, T> = new Map();
    const queue: T[] = [source];
    for (let i = 0; i < queue.length; i++) {
      const w = queue[i];
      const outs = this._out.get(w);
      if (outs === undefined) continue;
      for (const v of outs.keys()) {
        if (!reach.has(v)) {
          reach.add(v);
          via.set(v, w);
          queue.push(v);
        } else if (sameValueZero(v, source) && !via.has(source)) {
          via.set(source, w);
        }
      }
    }
    return { reach, via };
  }

  private _markAdded(u: T, v: T): void {
    const del = this._netDel.get(u);
    if (del !== undefined && del.delete(v)) {
      if (del.size === 0) this._netDel.delete(u);
      return;
    }
    let set = this._netAdd.get(u);
    if (set === undefined) {
      set = new Set();
      this._netAdd.set(u, set);
    }
    set.add(v);
  }

  private _markRemoved(u: T, v: T): void {
    const add = this._netAdd.get(u);
    if (add !== undefined && add.delete(v)) {
      if (add.size === 0) this._netAdd.delete(u);
      return;
    }
    let set = this._netDel.get(u);
    if (set === undefined) {
      set = new Set();
      this._netDel.set(u, set);
    }
    set.add(v);
  }
}
