import { GENESIS_HASH, valueEquals, valueToString } from './types.ts';
import type { Ballot, LogEntry, NodeId } from './types.ts';

export interface NodeSnapshot {
  id: NodeId;
  promised: Ballot;
  log: LogEntry[];
  committed: LogEntry[];
  commitIndex: number;
  lease: { leaderId: NodeId; until: number } | null;
}

export interface ClusterSnapshot {
  nodes: NodeSnapshot[];
  quorum: number;
}

export type FindingKind =
  | 'DANGLING_REF'
  | 'ORPHAN_LOOP'
  | 'DATA_SKEW'
  | 'COMMITTED_CONFLICT';

export interface Finding {
  kind: FindingKind;
  node: NodeId | 'cluster';
  detail: string;
}

export interface InspectionReport {
  ok: boolean;
  danglingRefs: Finding[];
  orphanLoops: Finding[];
  dataSkew: Finding[];
  committedConflicts: Finding[];
}

export interface InspectOptions {
  /** Relative deviation from the mean log size that counts as skew. Default 0.5. */
  skewThreshold?: number;
}

interface SnapshotSource {
  readonly id: NodeId;
  readonly promised: Ballot;
  readonly log: ReadonlyMap<number, LogEntry>;
  readonly committed: ReadonlyMap<number, LogEntry>;
  readonly commitIndex: number;
  leaseValid(): boolean;
  readonly leaseUntil: number;
}

export function snapshotFromNodes(nodes: SnapshotSource[], quorum: number): ClusterSnapshot {
  return {
    quorum,
    nodes: nodes.map((n) => ({
      id: n.id,
      promised: n.promised,
      log: [...n.log.values()].sort((a, b) => a.slot - b.slot),
      committed: [...n.committed.values()].sort((a, b) => a.slot - b.slot),
      commitIndex: n.commitIndex,
      lease: n.leaseValid() ? { leaderId: n.id, until: n.leaseUntil } : null,
    })),
  };
}

/**
 * Offline full-state integrity inspector. Scans a cluster snapshot for:
 *  - dangling references (hash-chain links to missing entries, commit
 *    watermarks beyond the committed log, leases pointing at unknown nodes)
 *  - orphan loops (reference cycles unreachable from any committed root)
 *  - data skew (log-size outliers, under-replicated committed slots)
 *  - committed conflicts (two different values decided at the same slot)
 */
export function inspectCluster(snapshot: ClusterSnapshot, options: InspectOptions = {}): InspectionReport {
  const skewThreshold = options.skewThreshold ?? 0.5;
  const danglingRefs: Finding[] = [];
  const orphanLoops: Finding[] = [];
  const dataSkew: Finding[] = [];
  const committedConflicts: Finding[] = [];

  const nodeIds = new Set(snapshot.nodes.map((n) => n.id));
  const knownHashes = new Set<string>([GENESIS_HASH]);
  for (const node of snapshot.nodes) {
    for (const entry of node.log) knownHashes.add(entry.hash);
    for (const entry of node.committed) knownHashes.add(entry.hash);
  }

  // ---- dangling references -------------------------------------------
  for (const node of snapshot.nodes) {
    const bySlot = new Map(node.log.map((e) => [e.slot, e]));
    for (const entry of node.log) {
      if (entry.slot === 1 && entry.prevHash !== GENESIS_HASH) {
        danglingRefs.push({
          kind: 'DANGLING_REF',
          node: node.id,
          detail: `slot 1 must anchor to ${GENESIS_HASH} but refers to ${entry.prevHash.slice(0, 12)}…`,
        });
        continue;
      }
      if (!knownHashes.has(entry.prevHash)) {
        danglingRefs.push({
          kind: 'DANGLING_REF',
          node: node.id,
          detail: `slot ${entry.slot} prevHash ${entry.prevHash.slice(0, 12)}… refers to a missing entry`,
        });
        continue;
      }
      if (entry.slot > 1) {
        const prev = bySlot.get(entry.slot - 1);
        if (!prev) {
          danglingRefs.push({
            kind: 'DANGLING_REF',
            node: node.id,
            detail: `slot ${entry.slot} exists but predecessor slot ${entry.slot - 1} is absent`,
          });
        } else if (prev.hash !== entry.prevHash) {
          danglingRefs.push({
            kind: 'DANGLING_REF',
            node: node.id,
            detail: `slot ${entry.slot} prevHash does not match entry at slot ${entry.slot - 1}`,
          });
        }
      }
    }
    const committedSlots = new Set(node.committed.map((e) => e.slot));
    for (let slot = 1; slot <= node.commitIndex; slot++) {
      if (!committedSlots.has(slot)) {
        danglingRefs.push({
          kind: 'DANGLING_REF',
          node: node.id,
          detail: `commitIndex ${node.commitIndex} refers to slot ${slot} which is not committed`,
        });
      }
    }
    if (node.lease && !nodeIds.has(node.lease.leaderId)) {
      danglingRefs.push({
        kind: 'DANGLING_REF',
        node: node.id,
        detail: `lease refers to unknown leader ${node.lease.leaderId}`,
      });
    }
  }

  // ---- orphan loops ----------------------------------------------------
  // Reference graph: entry -> prevHash, node -> committed tip, node -> lease leader.
  const edges = new Map<string, string>();
  const grounded = new Set<string>();
  for (const node of snapshot.nodes) {
    for (const entry of node.log) {
      if (entry.prevHash !== GENESIS_HASH && knownHashes.has(entry.prevHash)) {
        edges.set(`h:${entry.hash}`, `h:${entry.prevHash}`);
      }
    }
    for (const entry of node.committed) grounded.add(`h:${entry.hash}`);
    const tip = node.committed.find((e) => e.slot === node.commitIndex);
    if (tip) edges.set(`n:${node.id}`, `h:${tip.hash}`);
    if (node.lease && node.lease.leaderId !== node.id && nodeIds.has(node.lease.leaderId)) {
      edges.set(`n:${node.id}`, `n:${node.lease.leaderId}`);
    }
  }

  const reachable = new Set<string>();
  const stack = [...grounded];
  while (stack.length > 0) {
    const v = stack.pop() as string;
    if (reachable.has(v)) continue;
    reachable.add(v);
    const next = edges.get(v);
    if (next) stack.push(next);
  }

  const color = new Map<string, 1 | 2>();
  const cycles: string[][] = [];
  const visit = (start: string): void => {
    const path: string[] = [];
    let v: string | undefined = start;
    while (v !== undefined) {
      const c = color.get(v);
      if (c === 2) return;
      if (c === 1) {
        const idx = path.indexOf(v);
        if (idx >= 0) cycles.push(path.slice(idx));
        for (const u of path) color.set(u, 2);
        return;
      }
      color.set(v, 1);
      path.push(v);
      v = edges.get(v);
    }
    for (const u of path) color.set(u, 2);
  };
  for (const vertex of edges.keys()) {
    if (!color.has(vertex)) visit(vertex);
  }
  for (const cycle of cycles) {
    const orphan = cycle.every((v) => !reachable.has(v));
    if (orphan) {
      orphanLoops.push({
        kind: 'ORPHAN_LOOP',
        node: 'cluster',
        detail: `reference cycle of ${cycle.length} vertex(es) unreachable from any committed root: ${cycle
          .map((v) => v.slice(2, 14))
          .join(' -> ')}`,
      });
    }
  }

  // ---- data skew -------------------------------------------------------
  const counts = snapshot.nodes.map((n) => n.log.length);
  if (counts.length > 0) {
    const mean = counts.reduce((a, b) => a + b, 0) / counts.length;
    if (mean > 0) {
      snapshot.nodes.forEach((node, i) => {
        const deviation = (counts[i] - mean) / mean;
        if (Math.abs(deviation) > skewThreshold) {
          dataSkew.push({
            kind: 'DATA_SKEW',
            node: node.id,
            detail: `log size ${counts[i]} deviates ${(deviation * 100).toFixed(0)}% from cluster mean ${mean.toFixed(1)}`,
          });
        }
      });
    }
  }
  const committedSlots = new Set<number>();
  for (const node of snapshot.nodes) for (const e of node.committed) committedSlots.add(e.slot);
  for (const slot of committedSlots) {
    let replication = 0;
    for (const node of snapshot.nodes) {
      if (node.log.some((e) => e.slot === slot) || node.committed.some((e) => e.slot === slot)) {
        replication++;
      }
    }
    if (replication < snapshot.quorum) {
      dataSkew.push({
        kind: 'DATA_SKEW',
        node: 'cluster',
        detail: `committed slot ${slot} replicated on only ${replication}/${snapshot.nodes.length} nodes (quorum ${snapshot.quorum})`,
      });
    }
  }

  // ---- committed conflicts --------------------------------------------
  const decided = new Map<number, LogEntry>();
  for (const node of snapshot.nodes) {
    for (const entry of node.committed) {
      const prev = decided.get(entry.slot);
      if (prev && !valueEquals(prev.value, entry.value)) {
        committedConflicts.push({
          kind: 'COMMITTED_CONFLICT',
          node: node.id,
          detail: `slot ${entry.slot} committed as ${valueToString(entry.value)} here but ${valueToString(prev.value)} elsewhere`,
        });
      } else if (!prev) {
        decided.set(entry.slot, entry);
      }
    }
  }

  return {
    ok:
      danglingRefs.length === 0 &&
      orphanLoops.length === 0 &&
      dataSkew.length === 0 &&
      committedConflicts.length === 0,
    danglingRefs,
    orphanLoops,
    dataSkew,
    committedConflicts,
  };
}
