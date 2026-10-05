import type {
  AbortResult,
  ApplyResult,
  CycleEdge,
  CycleReport,
  DetectorEvent,
  TxnId,
} from "./types.ts";
import { VersionedStore } from "./versioned-store.ts";

function edgeKey(partition: string, from: string, to: string): string {
  return JSON.stringify([partition, from, to]);
}

function txnKey(partition: string, txn: string): string {
  return JSON.stringify([partition, txn]);
}

function fingerprintOf(edges: CycleEdge[]): string {
  return edges.map((e) => `${e.partition}|${e.from}|${e.to}|${e.version}`).join(";");
}

function compareEdges(a: CycleEdge, b: CycleEdge): number {
  return (
    a.from.localeCompare(b.from) ||
    a.to.localeCompare(b.to) ||
    a.partition.localeCompare(b.partition)
  );
}

function buildAdjacency(edgeList: CycleEdge[]): Map<TxnId, CycleEdge[]> {
  const adj = new Map<TxnId, CycleEdge[]>();
  for (const e of edgeList) {
    const list = adj.get(e.from);
    if (list) list.push(e);
    else adj.set(e.from, [e]);
  }
  for (const list of adj.values()) list.sort(compareEdges);
  return adj;
}

/** BFS shortest path from `src` to `dst`, returned as an edge list. */
function shortestPath(
  adj: Map<TxnId, CycleEdge[]>,
  src: TxnId,
  dst: TxnId,
): CycleEdge[] | null {
  if (src === dst) return [];
  const prev = new Map<TxnId, { from: TxnId; via: CycleEdge }>();
  const visited = new Set<TxnId>([src]);
  const queue: TxnId[] = [src];
  while (queue.length > 0) {
    const cur = queue.shift() as TxnId;
    for (const e of adj.get(cur) ?? []) {
      if (visited.has(e.to)) continue;
      visited.add(e.to);
      prev.set(e.to, { from: cur, via: e });
      if (e.to === dst) {
        const path: CycleEdge[] = [];
        let node = dst;
        while (node !== src) {
          const step = prev.get(node);
          if (!step) return null;
          path.unshift(step.via);
          node = step.from;
        }
        return path;
      }
      queue.push(e.to);
    }
  }
  return null;
}

/**
 * Shortest directed cycle over the given edges, or null when acyclic.
 * Deterministic: edges are scanned in sorted order and the first
 * strictly-shortest cycle wins. Self-loops are assumed filtered out.
 */
export function findShortestCycleInEdges(
  edgeList: CycleEdge[],
): { nodes: TxnId[]; edges: CycleEdge[] } | null {
  const adj = buildAdjacency(edgeList);
  const sorted = [...edgeList].sort(compareEdges);
  let best: { nodes: TxnId[]; edges: CycleEdge[] } | null = null;
  for (const e of sorted) {
    if (best !== null && best.edges.length <= 2) break; // cannot beat a 2-cycle
    const path = shortestPath(adj, e.to, e.from);
    if (path === null) continue;
    const cycleEdges = [e, ...path];
    if (best === null || cycleEdges.length < best.edges.length) {
      const nodes = [e.from, ...cycleEdges.slice(0, -1).map((x) => x.to)];
      best = { nodes, edges: cycleEdges };
    }
  }
  return best;
}

/**
 * Global wait-for graph aggregating versioned edge/liveness events from
 * every partition. Local graphs may each be acyclic while the merged
 * graph contains a cross-partition cycle; detection always runs on the
 * merged view.
 */
export class WaitGraph {
  private edges = new VersionedStore<string>();
  private txns = new VersionedStore<string>();

  apply(event: DetectorEvent): ApplyResult {
    switch (event.kind) {
      case "edge-upsert":
      case "edge-remove": {
        if (event.from === event.to) return { applied: false, reason: "self-loop-ignored" };
        const key = edgeKey(event.partition, event.from, event.to);
        const ok = this.edges.apply(key, event.version, event.kind === "edge-upsert");
        return ok ? { applied: true } : { applied: false, reason: "stale-version" };
      }
      case "txn-begin":
      case "txn-end": {
        const key = txnKey(event.partition, event.txn);
        const ok = this.txns.apply(key, event.version, event.kind === "txn-begin");
        return ok ? { applied: true } : { applied: false, reason: "stale-version" };
      }
    }
  }

  applyAll(events: Iterable<DetectorEvent>): { applied: number; dropped: number } {
    let applied = 0;
    let dropped = 0;
    for (const event of events) {
      if (this.apply(event).applied) applied++;
      else dropped++;
    }
    return { applied, dropped };
  }

  isAlive(txn: TxnId): boolean {
    for (const { key } of this.txns.liveEntries()) {
      const [, id] = JSON.parse(key) as [string, string];
      if (id === txn) return true;
    }
    return false;
  }

  liveTxns(): Set<TxnId> {
    const out = new Set<TxnId>();
    for (const { key } of this.txns.liveEntries()) {
      out.add((JSON.parse(key) as [string, string])[1]);
    }
    return out;
  }

  liveEdges(): CycleEdge[] {
    return this.edges.liveEntries().map(({ key, version }) => {
      const [partition, from, to] = JSON.parse(key) as [string, string, string];
      return { partition, from, to, version };
    });
  }

  /** Shortest cycle in the merged global graph, with edge versions. */
  detect(): CycleReport | null {
    const cycle = findShortestCycleInEdges(this.liveEdges());
    if (cycle === null) return null;
    return {
      length: cycle.edges.length,
      nodes: cycle.nodes,
      edges: cycle.edges,
      fingerprint: fingerprintOf(cycle.edges),
    };
  }

  /**
   * True when every edge of the reported cycle still exists with the
   * exact version captured at detection time.
   */
  isSnapshotFresh(report: CycleReport): { fresh: boolean; detail?: string } {
    for (const e of report.edges) {
      const key = edgeKey(e.partition, e.from, e.to);
      if (!this.edges.isPresent(key)) {
        return {
          fresh: false,
          detail: `edge ${e.from}->${e.to}@${e.partition} v${e.version} was removed`,
        };
      }
      const current = this.edges.versionOf(key);
      if (current !== e.version) {
        return {
          fresh: false,
          detail: `edge ${e.from}->${e.to}@${e.partition} changed v${e.version} -> v${current}`,
        };
      }
    }
    return { fresh: true };
  }

  /**
   * Pick a victim among the cycle members that are still alive.
   * Lower cost wins; defaults to lexicographic order for determinism.
   * Returns null when no cycle member is alive.
   */
  suggestVictim(report: CycleReport, cost?: (txn: TxnId) => number): TxnId | null {
    const candidates = report.nodes.filter((n) => this.isAlive(n)).sort();
    if (candidates.length === 0) return null;
    if (!cost) return candidates[0];
    let best = candidates[0];
    for (const c of candidates) {
      if (cost(c) < cost(best)) best = c;
    }
    return best;
  }

  /**
   * Abort a victim, but only when the snapshot that implicated it is
   * still fresh and the victim is still alive. A stale snapshot or a
   * dead victim is a no-op, so outdated reports can never cause a
   * false kill.
   */
  abort(report: CycleReport, victim: TxnId): AbortResult {
    if (!report.nodes.includes(victim)) {
      return { status: "victim-not-in-cycle", victim };
    }
    if (!this.isAlive(victim)) {
      return { status: "victim-not-live", victim };
    }
    const freshness = this.isSnapshotFresh(report);
    if (!freshness.fresh) {
      return { status: "stale-snapshot", victim, detail: freshness.detail ?? "" };
    }
    this.endTxnLocally(victim);
    return { status: "aborted", victim };
  }

  /**
   * Record the abort locally as a versioned txn-end tombstone, so the
   * victim immediately leaves the live set. Later partition events with
   * higher versions still apply normally.
   */
  private endTxnLocally(txn: TxnId): void {
    for (const { key, version } of this.txns.liveEntries()) {
      const [, id] = JSON.parse(key) as [string, string];
      if (id === txn) this.txns.apply(key, version + 1, false);
    }
  }

  stats(): { liveEdges: number; liveTxns: number; droppedEdgeEvents: number; droppedTxnEvents: number } {
    return {
      liveEdges: this.edges.liveEntries().length,
      liveTxns: this.liveTxns().size,
      droppedEdgeEvents: this.edges.dropped(),
      droppedTxnEvents: this.txns.dropped(),
    };
  }
}
