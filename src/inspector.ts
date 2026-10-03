import { GENESIS_PARENT, compareBallots } from './types.ts';
import type { Ballot, NodeId } from './types.ts';
import type { ClusterSnapshot, SnapshotNode } from './cluster.ts';

export interface DanglingRef {
  nodeId: NodeId;
  kind:
    | 'parent-slot'
    | 'non-contiguous-parent'
    | 'commit-pointer'
    | 'leader-ref'
    | 'lease';
  detail: string;
}

export interface OrphanCycle {
  kind: 'log-chain' | 'leadership';
  members: string[];
  detail: string;
}

export interface SkewFinding {
  kind: 'log-size' | 'replication-lag' | 'dirty-concentration' | 'commit-coverage';
  detail: string;
  magnitude: number;
}

export interface InvariantViolation {
  invariant: string;
  detail: string;
}

export interface InspectOptions {
  /** log-size skew is flagged when max/min exceeds this ratio (default 2). */
  maxSizeRatio?: number;
  /** ...or when the absolute lag exceeds this many slots (default 3). */
  maxAbsLag?: number;
  /** dirty-entry share on a single node above this is skew (default 0.5). */
  maxDirtyShare?: number;
  /** leadership history for election-safety checks: (term -> leader). */
  leadershipHistory?: { term: number; nodeId: NodeId }[];
}

export interface InspectionReport {
  danglingRefs: DanglingRef[];
  orphanCycles: OrphanCycle[];
  skew: SkewFinding[];
  invariantViolations: InvariantViolation[];
  ok: boolean;
}

function checkDanglingRefs(
  snap: ClusterSnapshot,
  out: DanglingRef[],
): void {
  const ids = new Set(snap.nodeIds);
  for (const node of snap.nodes) {
    const slots = new Map(node.log.map((e) => [e.slot, e]));
    for (const e of node.log) {
      if (e.parentSlot !== GENESIS_PARENT && !slots.has(e.parentSlot)) {
        out.push({
          nodeId: node.id,
          kind: 'parent-slot',
          detail: `slot ${e.slot} references missing parent slot ${e.parentSlot}`,
        });
      }
      if (e.parentSlot !== GENESIS_PARENT && e.parentSlot !== e.slot - 1) {
        out.push({
          nodeId: node.id,
          kind: 'non-contiguous-parent',
          detail: `slot ${e.slot} has non-contiguous parent ${e.parentSlot}`,
        });
      }
    }
    if (node.commitIndex >= 0) {
      const tip = slots.get(node.commitIndex);
      if (!tip || !tip.committed) {
        out.push({
          nodeId: node.id,
          kind: 'commit-pointer',
          detail: `commitIndex ${node.commitIndex} does not point at a committed slot`,
        });
      }
    }
    if (node.knownLeader !== null && !ids.has(node.knownLeader)) {
      out.push({
        nodeId: node.id,
        kind: 'leader-ref',
        detail: `knownLeader '${node.knownLeader}' is not a cluster member`,
      });
    }
    if (node.role === 'leader' && node.leaseExpiry < snap.time) {
      out.push({
        nodeId: node.id,
        kind: 'lease',
        detail: `role=leader but lease expired at ${node.leaseExpiry} (now ${snap.time})`,
      });
    }
  }
}

function checkOrphanCycles(
  snap: ClusterSnapshot,
  out: OrphanCycle[],
): void {
  // Log-chain cycles: parent references must form a chain to genesis.
  for (const node of snap.nodes) {
    const bySlot = new Map(node.log.map((e) => [e.slot, e]));
    const state = new Map<number, 'visiting' | 'done'>();
    for (const e of node.log) {
      if (state.has(e.slot)) continue;
      const path: number[] = [];
      let cur: number | undefined = e.slot;
      while (cur !== undefined && cur !== GENESIS_PARENT) {
        const mark = state.get(cur);
        if (mark === 'done') break;
        if (mark === 'visiting') {
          const cycle = path.slice(path.indexOf(cur));
          out.push({
            kind: 'log-chain',
            members: cycle.map((s) => `${node.id}#${s}`),
            detail: `parent references form a cycle of ${cycle.length} slot(s) detached from genesis`,
          });
          break;
        }
        state.set(cur, 'visiting');
        path.push(cur);
        const rec = bySlot.get(cur);
        cur = rec ? rec.parentSlot : undefined;
      }
      for (const s of path) state.set(s, 'done');
    }
  }
  // Leadership cycles: knownLeader pointers that loop without any member
  // actually holding the leadership role are orphaned belief loops.
  const edges = new Map<NodeId, NodeId>();
  const roles = new Map<NodeId, SnapshotNode['role']>();
  for (const n of snap.nodes) {
    roles.set(n.id, n.role);
    if (n.knownLeader !== null) edges.set(n.id, n.knownLeader);
  }
  const seen = new Set<NodeId>();
  for (const start of edges.keys()) {
    if (seen.has(start)) continue;
    const path: NodeId[] = [];
    let cur: NodeId | undefined = start;
    while (cur !== undefined && edges.has(cur) && !seen.has(cur)) {
      seen.add(cur);
      path.push(cur);
      cur = edges.get(cur);
    }
    if (cur !== undefined && path.includes(cur)) {
      const cycle = path.slice(path.indexOf(cur));
      const anyRealLeader = cycle.some((id) => roles.get(id) === 'leader');
      if (!anyRealLeader) {
        out.push({
          kind: 'leadership',
          members: cycle,
          detail: `leadership belief cycle with no actual leader: ${cycle.join(' -> ')}`,
        });
      }
    }
  }
}

function checkSkew(
  snap: ClusterSnapshot,
  opts: Required<InspectOptions>,
  out: SkewFinding[],
): void {
  if (snap.nodes.length === 0) return;
  const sizes = snap.nodes.map((n) => n.log.length);
  const max = Math.max(...sizes);
  const min = Math.min(...sizes);
  const ratio = min === 0 ? (max === 0 ? 1 : Infinity) : max / min;
  if (ratio > opts.maxSizeRatio || max - min > opts.maxAbsLag) {
    out.push({
      kind: 'log-size',
      detail: `log sizes range ${min}..${max} (ratio ${ratio === Infinity ? 'inf' : ratio.toFixed(2)}, lag ${max - min})`,
      magnitude: max - min,
    });
  }
  // Replication lag: committed slots missing on followers.
  const committedSlots = new Set<number>();
  for (const n of snap.nodes) {
    for (const e of n.log) if (e.committed) committedSlots.add(e.slot);
  }
  let worstLag = 0;
  for (const n of snap.nodes) {
    const held = new Set(n.log.filter((e) => e.committed).map((e) => e.slot));
    const missing = [...committedSlots].filter((s) => !held.has(s)).length;
    worstLag = Math.max(worstLag, missing);
  }
  if (worstLag > opts.maxAbsLag) {
    out.push({
      kind: 'replication-lag',
      detail: `a node is missing ${worstLag} committed slot(s)`,
      magnitude: worstLag,
    });
  }
  // Dirty concentration: uncommitted (accepted-only) entries piling up on
  // a single node indicate a stranded minority.
  const dirtyByNode = snap.nodes.map(
    (n) => ({ id: n.id, dirty: n.log.filter((e) => !e.committed).length }),
  );
  const totalDirty = dirtyByNode.reduce((a, b) => a + b.dirty, 0);
  if (totalDirty > 0) {
    for (const d of dirtyByNode) {
      const share = d.dirty / totalDirty;
      if (d.dirty > 0 && share > opts.maxDirtyShare && totalDirty > 1) {
        out.push({
          kind: 'dirty-concentration',
          detail: `node ${d.id} holds ${d.dirty}/${totalDirty} (${(share * 100).toFixed(0)}%) of all uncommitted entries`,
          magnitude: share,
        });
      }
    }
  }
  // Commit coverage: a committed entry must live on at least a majority.
  const holders = new Map<number, number>();
  for (const n of snap.nodes) {
    for (const e of n.log) {
      if (e.committed) holders.set(e.slot, (holders.get(e.slot) ?? 0) + 1);
    }
  }
  for (const [slot, count] of holders) {
    if (count < snap.majority) {
      out.push({
        kind: 'commit-coverage',
        detail: `committed slot ${slot} is held by only ${count}/${snap.nodes.length} nodes (majority=${snap.majority})`,
        magnitude: snap.majority - count,
      });
    }
  }
}

function checkInvariants(
  snap: ClusterSnapshot,
  opts: Required<InspectOptions>,
  out: InvariantViolation[],
): void {
  // I1 election safety: leadership is per-ballot (term, nodeId); the same
  // ballot must never be claimed twice, and two distinct live leaders may
  // never hold the identical ballot.
  if (opts.leadershipHistory) {
    const claimed = new Map<string, number>();
    for (const h of opts.leadershipHistory) {
      const key = `${h.term}:${h.nodeId}`;
      const count = (claimed.get(key) ?? 0) + 1;
      claimed.set(key, count);
      if (count > 1) {
        out.push({
          invariant: 'I1 election-safety',
          detail: `ballot (${h.term},${h.nodeId}) was claimed as leader ${count} times`,
        });
      }
    }
  }
  const liveLeaders = snap.nodes.filter(
    (n) => n.role === 'leader' && n.leaseExpiry >= snap.time,
  );
  const liveBallots = new Map<string, NodeId>();
  for (const n of liveLeaders) {
    if (!n.promised) continue;
    const key = `${n.promised.term}:${n.promised.nodeId}`;
    const prev = liveBallots.get(key);
    if (prev !== undefined && prev !== n.id) {
      out.push({
        invariant: 'I1 election-safety',
        detail: `ballot (${n.promised.term},${n.promised.nodeId}) is held by both ${prev} and ${n.id}`,
      });
    }
    liveBallots.set(key, n.id);
  }
  // I2 log matching: same (slot, ballot) => same value everywhere.
  const bySlotBallot = new Map<string, { value: string; nodeId: NodeId }>();
  for (const n of snap.nodes) {
    for (const e of n.log) {
      const key = `${e.slot}@${e.ballot.term}:${e.ballot.nodeId}`;
      const prev = bySlotBallot.get(key);
      if (prev && prev.value !== e.value) {
        out.push({
          invariant: 'I2 log-matching',
          detail: `slot ${e.slot} ballot (${e.ballot.term},${e.ballot.nodeId}) is '${prev.value}' on ${prev.nodeId} but '${e.value}' on ${n.id}`,
        });
      }
      bySlotBallot.set(key, { value: e.value, nodeId: n.id });
    }
  }
  // I3 state machine safety: no two nodes commit different values per slot.
  const committed = new Map<number, { value: string; nodeId: NodeId }>();
  for (const n of snap.nodes) {
    for (const e of n.log) {
      if (!e.committed) continue;
      const prev = committed.get(e.slot);
      if (prev && prev.value !== e.value) {
        out.push({
          invariant: 'I3 state-machine-safety',
          detail: `slot ${e.slot} committed as '${prev.value}' on ${prev.nodeId} but '${e.value}' on ${n.id}`,
        });
      }
      committed.set(e.slot, { value: e.value, nodeId: n.id });
    }
  }
  // I4 gap-free commit: committed slots per node are exactly 0..commitIndex.
  for (const n of snap.nodes) {
    const committedSet = new Set(
      n.log.filter((e) => e.committed).map((e) => e.slot),
    );
    for (let s = 0; s <= n.commitIndex; s++) {
      if (!committedSet.has(s)) {
        out.push({
          invariant: 'I4 gap-free-commit',
          detail: `node ${n.id} has commitIndex ${n.commitIndex} but slot ${s} is not committed`,
        });
        break;
      }
    }
    for (const s of committedSet) {
      if (s > n.commitIndex) {
        out.push({
          invariant: 'I4 gap-free-commit',
          detail: `node ${n.id} committed slot ${s} beyond commitIndex ${n.commitIndex}`,
        });
        break;
      }
    }
  }
  // I5 leader completeness (offline form): the live leader with the
  // highest term must already hold every value committed anywhere.
  const committedBySlot = new Map<number, string>();
  for (const [slot, c] of committed) committedBySlot.set(slot, c.value);
  let best: SnapshotNode | null = null;
  for (const n of liveLeaders) {
    if (!n.promised) continue;
    if (
      !best ||
      !best.promised ||
      compareBallots(n.promised, best.promised) > 0
    ) {
      best = n;
    }
  }
  if (best) {
    const bySlot = new Map(best.log.map((e) => [e.slot, e]));
    for (const [slot, value] of committedBySlot) {
      const e = bySlot.get(slot);
      if (!e || e.value !== value) {
        out.push({
          invariant: 'I5 leader-completeness',
          detail: `leader ${best.id} (term ${best.promised!.term}) lacks committed value '${value}' at slot ${slot}`,
        });
      }
    }
  }
}

const DEFAULTS: Required<InspectOptions> = {
  maxSizeRatio: 2,
  maxAbsLag: 3,
  maxDirtyShare: 0.5,
  leadershipHistory: [],
};

/**
 * Offline full-state integrity scan. Pure function over a snapshot:
 * never mutates cluster state, safe to run at any quiescent point.
 */
export function inspect(
  snap: ClusterSnapshot,
  options: InspectOptions = {},
): InspectionReport {
  const opts: Required<InspectOptions> = {
    ...DEFAULTS,
    ...options,
    leadershipHistory: options.leadershipHistory ?? [],
  };
  const report: InspectionReport = {
    danglingRefs: [],
    orphanCycles: [],
    skew: [],
    invariantViolations: [],
    ok: false,
  };
  checkDanglingRefs(snap, report.danglingRefs);
  checkOrphanCycles(snap, report.orphanCycles);
  checkSkew(snap, opts, report.skew);
  checkInvariants(snap, opts, report.invariantViolations);
  report.ok =
    report.danglingRefs.length === 0 &&
    report.orphanCycles.length === 0 &&
    report.invariantViolations.length === 0;
  return report;
}
