/**
 * Incremental max-flow adjuster for directed networks with integer capacities.
 *
 * Supports batch capacity increases/decreases on existing edges. When a
 * capacity reduction makes the current flow infeasible, the excess flow is
 * withdrawn along residual (reverse) augmenting tracks, then alternative
 * augmenting paths are searched to recover flow. After every change a min-cut
 * certificate (S-T node partition + cut edge set) is produced.
 */
interface Arc {
  to: number;
  rev: number; // index of the reverse arc in adj[to]
  cap: number; // residual capacity
}

export interface CutEdge {
  u: string;
  v: string;
  capacity: number;
  flow: number;
}

export interface CutCertificate {
  maxFlow: number;
  sourceSide: string[];
  sinkSide: string[];
  cutEdges: CutEdge[];
  cutCapacity: number;
}

export interface CapacityChange {
  edge: number;
  capacity: number;
}

export interface UpdateReport {
  maxFlow: number;
  withdrawn: number;
  reaugmented: number;
  certificate: CutCertificate;
}

export interface EdgeSnapshot {
  id: number;
  u: string;
  v: string;
  capacity: number;
  flow: number;
}

interface EdgeRecord {
  u: number;
  v: number;
  capacity: number;
  arcIndex: number; // index of the forward arc in adj[u]
}

export class IncrementalMaxFlow {
  private names: string[] = [];
  private index = new Map<string, number>();
  private adj: Arc[][] = [];
  private edges: EdgeRecord[] = [];
  private readonly s: number;
  private readonly t: number;
  private flowValue = 0;
  public readonly source: string;
  public readonly sink: string;

  constructor(source: string, sink: string) {
    if (source === sink) throw new Error("source and sink must differ");
    this.source = source;
    this.sink = sink;
    this.s = this.nodeIndex(source);
    this.t = this.nodeIndex(sink);
  }

  private nodeIndex(name: string): number {
    let i = this.index.get(name);
    if (i === undefined) {
      i = this.names.length;
      this.names.push(name);
      this.index.set(name, i);
      this.adj.push([]);
    }
    return i;
  }

  addEdge(u: string, v: string, capacity: number): number {
    if (!Number.isInteger(capacity) || capacity < 0) {
      throw new Error(`capacity must be a non-negative integer, got ${capacity}`);
    }
    const ui = this.nodeIndex(u);
    const vi = this.nodeIndex(v);
    const arcIndex = this.adj[ui].length;
    this.adj[ui].push({ to: vi, rev: this.adj[vi].length, cap: capacity });
    this.adj[vi].push({ to: ui, rev: arcIndex, cap: 0 });
    const id = this.edges.length;
    this.edges.push({ u: ui, v: vi, capacity, arcIndex });
    return id;
  }

  getEdge(id: number): EdgeSnapshot {
    const e = this.edges[id];
    if (!e) throw new Error(`unknown edge id ${id}`);
    return {
      id,
      u: this.names[e.u],
      v: this.names[e.v],
      capacity: e.capacity,
      flow: this.flowOn(e),
    };
  }

  snapshot(): EdgeSnapshot[] {
    return this.edges.map((_, id) => this.getEdge(id));
  }

  private flowOn(e: EdgeRecord): number {
    const fwd = this.adj[e.u][e.arcIndex];
    return this.adj[e.v][fwd.rev].cap;
  }

  /** Initial max-flow computation from a zero flow. */
  computeMaxFlow(): CutCertificate {
    this.flowValue = 0;
    this.flowValue += this.dinic();
    return this.certificate();
  }

  /**
   * Apply a batch of capacity changes to existing edges, then repair the flow:
   * withdraw excess flow along reverse residual tracks where reductions make
   * the flow infeasible, and re-augment to recover flow via alternative paths.
   */
  updateCapacities(changes: CapacityChange[]): UpdateReport {
    let withdrawn = 0;
    for (const change of changes) {
      const e = this.edges[change.edge];
      if (!e) throw new Error(`unknown edge id ${change.edge}`);
      if (!Number.isInteger(change.capacity) || change.capacity < 0) {
        throw new Error(`capacity must be a non-negative integer, got ${change.capacity}`);
      }
      const delta = change.capacity - e.capacity;
      if (delta === 0) continue;
      e.capacity = change.capacity;
      const fwd = this.adj[e.u][e.arcIndex];
      fwd.cap += delta;
      if (fwd.cap >= 0) continue; // increase, or decrease that stays feasible

      // Flow on (u,v) exceeds the new capacity: withdraw the excess.
      const excess = -fwd.cap;
      fwd.cap = 0;
      const bwd = this.adj[e.v][fwd.rev];
      bwd.cap -= excess; // cancel excess flow on the edge itself

      // Restore conservation: u now has a surplus, v a deficit. Push the
      // surplus from u back to the source and pull the deficit back from the
      // sink to v, both along residual (reverse) tracks of the current flow.
      this.pushBack(e.u, this.s, excess);
      this.pushBack(this.t, e.v, excess);
      this.flowValue -= excess;
      withdrawn += excess;
    }
    const reaugmented = this.dinic();
    this.flowValue += reaugmented;
    return {
      maxFlow: this.flowValue,
      withdrawn,
      reaugmented,
      certificate: this.certificate(),
    };
  }

  /**
   * Push `amount` units from `from` to `to` through the residual network.
   * Used to withdraw flow: paths follow backward arcs of the current flow,
   * so they always exist with sufficient capacity by flow decomposition.
   */
  private pushBack(from: number, to: number, amount: number): void {
    let remaining = amount;
    while (remaining > 0) {
      if (from === to) return;
      const prev = this.bfsPath(from, to);
      if (!prev) {
        throw new Error(
          `no residual path from ${this.names[from]} to ${this.names[to]} to withdraw flow`,
        );
      }
      let bottleneck = remaining;
      for (let v = to; v !== from; v = prev[v]!.from) {
        bottleneck = Math.min(bottleneck, prev[v]!.arc.cap);
      }
      for (let v = to; v !== from; v = prev[v]!.from) {
        const arc = prev[v]!.arc;
        arc.cap -= bottleneck;
        this.adj[arc.to][arc.rev].cap += bottleneck;
      }
      remaining -= bottleneck;
    }
  }

  private bfsPath(from: number, to: number): Array<{ from: number; arc: Arc } | null> | null {
    const prev: Array<{ from: number; arc: Arc } | null> = new Array(this.adj.length).fill(null);
    const seen = new Array<boolean>(this.adj.length).fill(false);
    seen[from] = true;
    const queue = [from];
    for (let head = 0; head < queue.length; head++) {
      const u = queue[head];
      if (u === to) return prev;
      for (const arc of this.adj[u]) {
        if (arc.cap > 0 && !seen[arc.to]) {
          seen[arc.to] = true;
          prev[arc.to] = { from: u, arc };
          queue.push(arc.to);
        }
      }
    }
    return seen[to] ? prev : null;
  }

  /** Dinic's algorithm on the current residual network; returns flow added. */
  private dinic(): number {
    const n = this.adj.length;
    let added = 0;
    for (;;) {
      const level = new Array<number>(n).fill(-1);
      level[this.s] = 0;
      const queue = [this.s];
      for (let head = 0; head < queue.length; head++) {
        const u = queue[head];
        for (const arc of this.adj[u]) {
          if (arc.cap > 0 && level[arc.to] < 0) {
            level[arc.to] = level[u] + 1;
            queue.push(arc.to);
          }
        }
      }
      if (level[this.t] < 0) return added;
      const next = new Array<number>(n).fill(0);
      const dfs = (u: number, f: number): number => {
        if (u === this.t) return f;
        for (; next[u] < this.adj[u].length; next[u]++) {
          const arc = this.adj[u][next[u]];
          if (arc.cap > 0 && level[arc.to] === level[u] + 1) {
            const pushed = dfs(arc.to, Math.min(f, arc.cap));
            if (pushed > 0) {
              arc.cap -= pushed;
              this.adj[arc.to][arc.rev].cap += pushed;
              return pushed;
            }
          }
        }
        return 0;
      };
      for (;;) {
        const pushed = dfs(this.s, Infinity);
        if (pushed === 0) break;
        added += pushed;
      }
    }
  }

  /** Min-cut certificate: BFS reachability in the residual network. */
  certificate(): CutCertificate {
    const n = this.adj.length;
    const seen = new Array<boolean>(n).fill(false);
    seen[this.s] = true;
    const queue = [this.s];
    for (let head = 0; head < queue.length; head++) {
      const u = queue[head];
      for (const arc of this.adj[u]) {
        if (arc.cap > 0 && !seen[arc.to]) {
          seen[arc.to] = true;
          queue.push(arc.to);
        }
      }
    }
    const sourceSide: string[] = [];
    const sinkSide: string[] = [];
    for (let i = 0; i < n; i++) {
      (seen[i] ? sourceSide : sinkSide).push(this.names[i]);
    }
    const cutEdges: CutEdge[] = [];
    let cutCapacity = 0;
    for (const e of this.edges) {
      if (seen[e.u] && !seen[e.v]) {
        cutEdges.push({
          u: this.names[e.u],
          v: this.names[e.v],
          capacity: e.capacity,
          flow: this.flowOn(e),
        });
        cutCapacity += e.capacity;
      }
    }
    return {
      maxFlow: this.flowValue,
      sourceSide,
      sinkSide,
      cutEdges,
      cutCapacity,
    };
  }
}
