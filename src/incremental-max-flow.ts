/**
 * Incremental max-flow adjuster with S-T min-cut certificate generation.
 *
 * Directed network with integer capacities. Supports batched capacity
 * increases / decreases on existing edges. When a capacity reduction makes the
 * current flow infeasible, the excess is withdrawn along the residual
 * network (reverse augmentation toward the source / from the sink), then
 * alternative augmenting paths are searched to restore a max flow.
 * Every change returns a fresh max-flow value plus an S-T cut certificate
 * (reachable partition + saturated cut edges).
 */

export interface CutEdge {
  id: number;
  from: number;
  to: number;
  capacity: number;
  flow: number;
}

export interface MinCutCertificate {
  /** Max-flow value after the change. */
  maxFlow: number;
  /** Nodes reachable from the source in the residual network (S side). */
  sourceSide: number[];
  /** Remaining nodes (T side, contains the sink). */
  sinkSide: number[];
  /** Original edges crossing S -> T; all saturated. Sum of capacities === maxFlow. */
  cutEdges: CutEdge[];
  cutCapacity: number;
}

export interface CapacityChange {
  edgeId: number;
  newCapacity: number;
}

export interface EdgeSnapshot {
  id: number;
  from: number;
  to: number;
  capacity: number;
  flow: number;
}

interface FlowEdge {
  id: number;
  from: number;
  to: number;
  capacity: number;
  flow: number;
}

interface ResidualStep {
  edgeId: number;
  forward: boolean;
}

export class IncrementalMaxFlow {
  private readonly nodeCount: number;
  private readonly source: number;
  private readonly sink: number;
  private readonly edges: FlowEdge[] = [];
  private readonly outgoing: number[][];
  private readonly incoming: number[][];
  private flowValue = 0;

  constructor(nodeCount: number, source: number, sink: number) {
    if (!Number.isInteger(nodeCount) || nodeCount < 2) {
      throw new RangeError("nodeCount must be an integer >= 2");
    }
    for (const [name, v] of [["source", source], ["sink", sink]] as const) {
      if (!Number.isInteger(v) || v < 0 || v >= nodeCount) {
        throw new RangeError(`${name} must be a node index in [0, ${nodeCount})`);
      }
    }
    if (source === sink) throw new RangeError("source and sink must differ");
    this.nodeCount = nodeCount;
    this.source = source;
    this.sink = sink;
    this.outgoing = Array.from({ length: nodeCount }, () => []);
    this.incoming = Array.from({ length: nodeCount }, () => []);
  }

  /** Adds a directed edge; returns its id used for later capacity updates. */
  addEdge(from: number, to: number, capacity: number): number {
    this.checkNode(from);
    this.checkNode(to);
    this.checkCapacity(capacity);
    if (from === to) throw new RangeError("self-loops are not allowed");
    const id = this.edges.length;
    this.edges.push({ id, from, to, capacity, flow: 0 });
    this.outgoing[from].push(id);
    this.incoming[to].push(id);
    return id;
  }

  /** Current max-flow value. */
  get value(): number {
    return this.flowValue;
  }

  getEdge(edgeId: number): EdgeSnapshot {
    return { ...this.edge(edgeId) };
  }

  /** Computes the max flow from scratch (Edmonds-Karp) and returns a certificate. */
  computeMaxFlow(): MinCutCertificate {
    for (const e of this.edges) e.flow = 0;
    this.flowValue = 0;
    this.augmentToMax();
    return this.certificate();
  }

  /** Increases an edge capacity, then restores max flow via new augmenting paths. */
  increaseCapacity(edgeId: number, delta: number): MinCutCertificate {
    this.checkDelta(delta);
    this.edge(edgeId).capacity += delta;
    this.augmentToMax();
    return this.certificate();
  }

  /**
   * Decreases an edge capacity. If the current flow on the edge exceeds the
   * new capacity, the excess is withdrawn along the residual network:
   * `excess` units are pushed from the edge tail back to the source and from
   * the sink back to the edge head (reverse augmentation), restoring flow
   * conservation. Alternative s-t paths are then searched to recover flow.
   */
  decreaseCapacity(edgeId: number, delta: number): MinCutCertificate {
    this.checkDelta(delta);
    const e = this.edge(edgeId);
    const newCapacity = e.capacity - delta;
    this.checkCapacity(newCapacity);
    e.capacity = newCapacity;
    if (e.flow > newCapacity) {
      const excess = e.flow - newCapacity;
      e.flow = newCapacity;
      // Tail `e.from` now has a surplus: drain it back to the source.
      this.withdraw(e.from, this.source, excess);
      // Head `e.to` now has a deficit: pull it back from the sink.
      this.withdraw(this.sink, e.to, excess);
      this.flowValue -= excess;
    }
    this.augmentToMax();
    return this.certificate();
  }

  /** Sets an absolute capacity; dispatches to increase/decrease. */
  setCapacity(edgeId: number, newCapacity: number): MinCutCertificate {
    const e = this.edge(edgeId);
    this.checkCapacity(newCapacity);
    const delta = newCapacity - e.capacity;
    if (delta > 0) return this.increaseCapacity(edgeId, delta);
    if (delta < 0) return this.decreaseCapacity(edgeId, -delta);
    return this.certificate();
  }

  /** Applies a batch of capacity changes; one certificate per change, in order. */
  applyBatch(changes: readonly CapacityChange[]): MinCutCertificate[] {
    return changes.map((c) => this.setCapacity(c.edgeId, c.newCapacity));
  }

  /** S-T min-cut certificate for the current flow. */
  certificate(): MinCutCertificate {
    const reachable = this.reachableFromSource();
    const sourceSide: number[] = [];
    const sinkSide: number[] = [];
    for (let v = 0; v < this.nodeCount; v++) {
      (reachable[v] ? sourceSide : sinkSide).push(v);
    }
    const cutEdges: CutEdge[] = [];
    let cutCapacity = 0;
    for (const e of this.edges) {
      if (reachable[e.from] && !reachable[e.to]) {
        cutEdges.push({ id: e.id, from: e.from, to: e.to, capacity: e.capacity, flow: e.flow });
        cutCapacity += e.capacity;
      }
    }
    return { maxFlow: this.flowValue, sourceSide, sinkSide, cutEdges, cutCapacity };
  }

  // ---- internals -------------------------------------------------------

  /** Repeatedly augments along shortest residual s-t paths until none remain. */
  private augmentToMax(): void {
    for (;;) {
      const path = this.bfsPath(this.source, this.sink);
      if (path === null) return;
      const pushed = this.push(path, Number.POSITIVE_INFINITY);
      this.flowValue += pushed;
    }
  }

  /**
   * Pushes `amount` units through the residual network from `from` to `to`.
   * Used for reverse flow withdrawal; paths exist because the pre-change
   * flow was feasible (flow decomposition guarantees residual routes).
   */
  private withdraw(from: number, to: number, amount: number): void {
    let remaining = amount;
    while (remaining > 0) {
      const path = this.bfsPath(from, to);
      if (path === null) {
        throw new Error(`cannot withdraw flow: no residual path from ${from} to ${to}`);
      }
      remaining -= this.push(path, remaining);
    }
  }

  /** Pushes up to `limit` units along `path`; returns the amount pushed. */
  private push(path: ResidualStep[], limit: number): number {
    let bottleneck = limit;
    for (const step of path) {
      bottleneck = Math.min(bottleneck, this.residualCapacity(step));
    }
    for (const step of path) {
      const e = this.edges[step.edgeId];
      e.flow += step.forward ? bottleneck : -bottleneck;
    }
    return bottleneck;
  }

  private residualCapacity(step: ResidualStep): number {
    const e = this.edges[step.edgeId];
    return step.forward ? e.capacity - e.flow : e.flow;
  }

  /** BFS over the residual network; returns the steps of a shortest path or null. */
  private bfsPath(from: number, to: number): ResidualStep[] | null {
    const prev: (ResidualStep | null)[] = new Array(this.nodeCount).fill(null);
    const visited = new Array(this.nodeCount).fill(false);
    visited[from] = true;
    const queue: number[] = [from];
    for (let head = 0; head < queue.length; head++) {
      const u = queue[head];
      if (u === to) break;
      for (const id of this.outgoing[u]) {
        const e = this.edges[id];
        if (!visited[e.to] && e.capacity - e.flow > 0) {
          visited[e.to] = true;
          prev[e.to] = { edgeId: id, forward: true };
          queue.push(e.to);
        }
      }
      for (const id of this.incoming[u]) {
        const e = this.edges[id];
        if (!visited[e.from] && e.flow > 0) {
          visited[e.from] = true;
          prev[e.from] = { edgeId: id, forward: false };
          queue.push(e.from);
        }
      }
    }
    if (!visited[to]) return null;
    const path: ResidualStep[] = [];
    for (let v = to; v !== from; ) {
      const step = prev[v]!;
      path.push(step);
      const e = this.edges[step.edgeId];
      v = step.forward ? e.from : e.to;
    }
    return path.reverse();
  }

  private reachableFromSource(): boolean[] {
    const visited = new Array(this.nodeCount).fill(false);
    visited[this.source] = true;
    const queue: number[] = [this.source];
    for (let head = 0; head < queue.length; head++) {
      const u = queue[head];
      for (const id of this.outgoing[u]) {
        const e = this.edges[id];
        if (!visited[e.to] && e.capacity - e.flow > 0) {
          visited[e.to] = true;
          queue.push(e.to);
        }
      }
      for (const id of this.incoming[u]) {
        const e = this.edges[id];
        if (!visited[e.from] && e.flow > 0) {
          visited[e.from] = true;
          queue.push(e.from);
        }
      }
    }
    return visited;
  }

  private edge(edgeId: number): FlowEdge {
    if (!Number.isInteger(edgeId) || edgeId < 0 || edgeId >= this.edges.length) {
      throw new RangeError(`unknown edge id: ${edgeId}`);
    }
    return this.edges[edgeId];
  }

  private checkNode(v: number): void {
    if (!Number.isInteger(v) || v < 0 || v >= this.nodeCount) {
      throw new RangeError(`node index out of range: ${v}`);
    }
  }

  private checkCapacity(c: number): void {
    if (!Number.isInteger(c) || c < 0) {
      throw new RangeError(`capacity must be a non-negative integer, got ${c}`);
    }
  }

  private checkDelta(d: number): void {
    if (!Number.isInteger(d) || d <= 0) {
      throw new RangeError(`delta must be a positive integer, got ${d}`);
    }
  }
}
