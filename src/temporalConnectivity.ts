/**
 * Temporal interval connectivity checker.
 *
 * Each edge is active on a half-open interval [start, end). Given a query
 * interval [queryStart, queryEnd), the checker determines whether two nodes
 * remain connected at EVERY instant of the query interval.
 *
 * Instead of sampling only the endpoints, the query interval is sliced at
 * every effective edge endpoint (clipped to the query range). Edge activity
 * is constant within each slice, so an independent BFS per slice is exact.
 * On the first failing slice, a cut witness is returned: the reachable set
 * from the source plus every edge crossing that cut (all inactive during
 * the slice), each annotated with why it is inactive.
 */

export interface TemporalEdge {
  readonly u: string;
  readonly v: string;
  /** Inclusive activation time. */
  readonly start: number;
  /** Exclusive deactivation time. */
  readonly end: number;
}

export interface CutEdgeEvidence {
  readonly edge: TemporalEdge;
  /** Why the edge is inactive during the failing slice. */
  readonly reason: 'expired' | 'not-yet-active';
}

export interface DisconnectReport {
  readonly connected: false;
  /** First minimal slice [sliceStart, sliceEnd) in which connectivity breaks. */
  readonly sliceStart: number;
  readonly sliceEnd: number;
  /** Nodes reachable from the source during the failing slice. */
  readonly reachable: readonly string[];
  /**
   * Cut witness: edges with exactly one endpoint in `reachable` that overlap
   * the query interval. None of them is active during the failing slice;
   * together they separate source from target.
   */
  readonly cut: readonly CutEdgeEvidence[];
}

export interface ConnectedReport {
  readonly connected: true;
  /** Number of slices verified by independent BFS. */
  readonly slicesChecked: number;
}

export type CheckResult = ConnectedReport | DisconnectReport;

export interface TimeSlice {
  readonly start: number;
  readonly end: number;
}

const overlaps = (start: number, end: number, lo: number, hi: number): boolean =>
  start < hi && end > lo;

/** Effective (never-active edges dropped) edges clipped-relevant to [lo, hi). */
function relevantEdges(edges: readonly TemporalEdge[], lo: number, hi: number): TemporalEdge[] {
  return edges.filter((e) => e.start < e.end && overlaps(e.start, e.end, lo, hi));
}

/**
 * All atomic slices of [queryStart, queryEnd) induced by the effective
 * endpoints of the given edges. Edge activity is constant within each slice.
 */
export function computeSlices(
  edges: readonly TemporalEdge[],
  queryStart: number,
  queryEnd: number,
): TimeSlice[] {
  const points = new Set<number>([queryStart, queryEnd]);
  for (const e of relevantEdges(edges, queryStart, queryEnd)) {
    points.add(Math.max(e.start, queryStart));
    points.add(Math.min(e.end, queryEnd));
  }
  const sorted = [...points].sort((a, b) => a - b);
  const slices: TimeSlice[] = [];
  for (let i = 0; i + 1 < sorted.length; i++) {
    slices.push({ start: sorted[i], end: sorted[i + 1] });
  }
  return slices;
}

/** Edges active throughout (equivalently: anywhere inside) the slice. */
function activeDuring(edges: readonly TemporalEdge[], slice: TimeSlice): TemporalEdge[] {
  return edges.filter((e) => e.start <= slice.start && e.end >= slice.end);
}

/** Undirected BFS from `source` over `active` edges. Returns the reachable set. */
function bfsReachable(source: string, active: readonly TemporalEdge[]): Set<string> {
  const adjacency = new Map<string, string[]>();
  for (const e of active) {
    (adjacency.get(e.u) ?? adjacency.set(e.u, []).get(e.u)!).push(e.v);
    (adjacency.get(e.v) ?? adjacency.set(e.v, []).get(e.v)!).push(e.u);
  }
  const seen = new Set<string>([source]);
  const queue: string[] = [source];
  for (let head = 0; head < queue.length; head++) {
    for (const next of adjacency.get(queue[head]) ?? []) {
      if (!seen.has(next)) {
        seen.add(next);
        queue.push(next);
      }
    }
  }
  return seen;
}

/**
 * Check whether `source` and `target` are connected at every instant of
 * [queryStart, queryEnd).
 *
 * - An empty query interval (queryStart === queryEnd) is vacuously connected.
 * - queryStart > queryEnd throws a RangeError.
 * - Edges are treated as undirected; degenerate edges (start >= end) never
 *   activate and are ignored.
 */
export function checkConnectivity(
  edges: readonly TemporalEdge[],
  source: string,
  target: string,
  queryStart: number,
  queryEnd: number,
): CheckResult {
  if (!Number.isFinite(queryStart) || !Number.isFinite(queryEnd)) {
    throw new RangeError('Query bounds must be finite numbers.');
  }
  if (queryStart > queryEnd) {
    throw new RangeError(`Empty/inverted query interval: [${queryStart}, ${queryEnd}).`);
  }
  if (queryStart === queryEnd || source === target) {
    return { connected: true, slicesChecked: 0 };
  }

  const relevant = relevantEdges(edges, queryStart, queryEnd);
  const slices = computeSlices(relevant, queryStart, queryEnd);

  for (const slice of slices) {
    const reachable = bfsReachable(source, activeDuring(relevant, slice));
    if (reachable.has(target)) continue;

    const cut: CutEdgeEvidence[] = [];
    for (const e of relevant) {
      const crossesCut = reachable.has(e.u) !== reachable.has(e.v);
      if (!crossesCut) continue;
      cut.push({
        edge: e,
        reason: e.end <= slice.start ? 'expired' : 'not-yet-active',
      });
    }
    return {
      connected: false,
      sliceStart: slice.start,
      sliceEnd: slice.end,
      reachable: [...reachable].sort(),
      cut,
    };
  }
  return { connected: true, slicesChecked: slices.length };
}
