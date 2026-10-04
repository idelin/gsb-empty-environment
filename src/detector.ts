import { findShortestCycle } from './cycle';
import { GlobalWaitGraph } from './globalGraph';
import { Cycle, TxnId, WaitEdge } from './types';

/** Point-in-time copy of the global wait-for graph, tagged with the coordinator epoch. */
export interface GraphSnapshot {
  epoch: number;
  edges: WaitEdge[];
}

export interface AbortSuggestion {
  victim: TxnId;
  cycle: Cycle;
  reason: string;
}

export type Diagnosis =
  | { status: 'no-deadlock' }
  | { status: 'deadlock'; cycle: Cycle; suggestion: AbortSuggestion | null }
  | { status: 'stale-snapshot'; cycle: Cycle };

export interface DetectorOptions {
  /** Liveness oracle: is this transaction still active right now? */
  isAlive: (txn: TxnId) => boolean;
  /** Abort cost estimate; lower is preferred as victim. Defaults to uniform cost. */
  abortCost?: (txn: TxnId) => number;
}

export class DeadlockDetector {
  constructor(
    private readonly graph: GlobalWaitGraph,
    private readonly options: DetectorOptions,
  ) {}

  snapshot(): GraphSnapshot {
    return { epoch: this.graph.epoch, edges: this.graph.snapshotEdges() };
  }

  /**
   * A snapshot is stale if the coordinator accepted any message after it was
   * taken, or if any edge it contains no longer exists with the same version.
   */
  isSnapshotStale(snap: GraphSnapshot): boolean {
    if (snap.epoch !== this.graph.epoch) return true;
    return snap.edges.some((e) => !this.graph.hasEdge(e.from, e.to, e.version));
  }

  /**
   * Diagnose the global wait-for state. Works on a fresh snapshot by default;
   * pass an explicit (possibly old) snapshot to check it — a stale snapshot
   * never yields an abort suggestion, so expired views cannot cause false kills.
   */
  diagnose(snap?: GraphSnapshot): Diagnosis {
    const s = snap ?? this.snapshot();
    const cycle = findShortestCycle(s.edges);
    if (!cycle) return { status: 'no-deadlock' };

    if (this.isSnapshotStale(s)) return { status: 'stale-snapshot', cycle };

    // Belt and braces: re-verify every cycle edge is still live with its version.
    const cycleStillValid = cycle.edges.every((e) =>
      this.graph.hasEdge(e.from, e.to, e.version),
    );
    if (!cycleStillValid) return { status: 'stale-snapshot', cycle };

    const suggestion = this.suggestVictim(cycle);
    return { status: 'deadlock', cycle, suggestion };
  }

  /**
   * Pick the cheapest transaction on the cycle that is *still alive*.
   * Transactions that already finished are never targeted. Returns null when
   * no live transaction remains on the cycle (nothing to abort).
   */
  private suggestVictim(cycle: Cycle): AbortSuggestion | null {
    const { isAlive, abortCost } = this.options;
    const candidates = cycle.nodes.filter((t) => isAlive(t));
    if (candidates.length === 0) return null;
    const cost = abortCost ?? (() => 1);
    let victim = candidates[0]!;
    for (const txn of candidates) {
      if (cost(txn) < cost(victim)) victim = txn;
    }
    return {
      victim,
      cycle,
      reason: `abort ${victim} to break cycle ${cycle.nodes.join(' -> ')} -> ${cycle.nodes[0]}`,
    };
  }
}

/** Human-readable rendering of a cycle including per-edge versions. */
export function formatCycle(cycle: Cycle): string {
  const parts: string[] = [];
  for (let i = 0; i < cycle.edges.length; i++) {
    const e = cycle.edges[i]!;
    parts.push(`${e.from} --[v${e.version}@${e.partition}]--> ${e.to}`);
  }
  return parts.join('\n');
}
